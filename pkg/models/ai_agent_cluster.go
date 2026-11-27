package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// aiAgentClusterTask is one operation of the ai agent cluster channel. A task
// reaches every node, but only the node hosting the room's agent (or the pending
// tool call) acts on it and replies.
type aiAgentClusterTask string

const (
	aiAgentClusterTaskStop            aiAgentClusterTask = "stop"
	aiAgentClusterTaskPause           aiAgentClusterTask = "pause"
	aiAgentClusterTaskResume          aiAgentClusterTask = "resume"
	aiAgentClusterTaskSendContext     aiAgentClusterTask = "send_context"
	aiAgentClusterTaskParticipantChat aiAgentClusterTask = "participant_chat"
	aiAgentClusterTaskSetLearner      aiAgentClusterTask = "set_learner"
	aiAgentClusterTaskCancelStopTimer aiAgentClusterTask = "cancel_stop_timer"
	aiAgentClusterTaskWhiteboardScene aiAgentClusterTask = "whiteboard_scene"
	aiAgentClusterTaskToolResult      aiAgentClusterTask = "tool_result"
)

// aiAgentContextSource labels a server-side event injected into a live session.
// Sources set by a client request stay free-form and are cast to this type.
type aiAgentContextSource string

const (
	aiAgentContextSourceCountdownTimer aiAgentContextSource = "countdown_timer"
	aiAgentContextSourcePoll           aiAgentContextSource = "poll"
)

// aiAgentClusterRequestTimeout bounds one cross-node request. It covers the end
// of a session: no node answers once the agent is gone.
const aiAgentClusterRequestTimeout = 5 * time.Second

// aiAgentClusterTaskPayload is one relayed ai agent operation. The protobuf
// payload carries the messages that only travel to the hosting node.
type aiAgentClusterTaskPayload struct {
	Task         aiAgentClusterTask   `json:"task"`
	RoomId       string               `json:"room_id"`
	UserId       string               `json:"user_id,omitempty"`
	UserName     string               `json:"user_name,omitempty"`
	Reason       string               `json:"reason,omitempty"`
	Text         string               `json:"text,omitempty"`
	Source       aiAgentContextSource `json:"source,omitempty"`
	Proactive    bool                 `json:"proactive,omitempty"`
	CancelTimer  bool                 `json:"cancel_timer,omitempty"`
	ProtoPayload []byte               `json:"proto_payload,omitempty"`
}

// HandleIncomingClusterTask is the core logic that runs on every server. A node
// without the room's agent simply ignores the task and does not reply.
func (m *AIAgentModel) HandleIncomingClusterTask(msg *nats.Msg) {
	payload := new(aiAgentClusterTaskPayload)
	if err := json.Unmarshal(msg.Data, payload); err != nil {
		m.logger.WithError(err).Error("failed to unmarshal the ai agent cluster task")
		return
	}
	log := m.logger.WithFields(logrus.Fields{"roomId": payload.RoomId, "task": payload.Task})

	reply := func(err error) {
		if msg.Reply == "" {
			return
		}
		res := &AgentTaskResponse{Status: err == nil}
		if err != nil {
			res.Msg = err.Error()
		}
		encoded, encodeErr := json.Marshal(res)
		if encodeErr != nil {
			log.WithError(encodeErr).Error("failed to encode the ai agent cluster reply")
			return
		}
		if err := m.publishCluster(msg.Reply, encoded); err != nil {
			log.WithError(err).Error("failed to publish the ai agent cluster reply")
		}
	}

	switch payload.Task {
	case aiAgentClusterTaskStop:
		if payload.CancelTimer {
			m.cancelStopTimer(payload.RoomId)
		}
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			agent.Stop(payload.Reason)
			reply(nil)
		}
	case aiAgentClusterTaskPause:
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			reply(m.pauseHostedAgent(payload.RoomId, agent))
		}
	case aiAgentClusterTaskResume:
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			reply(m.resumeHostedAgent(payload.RoomId, agent))
		}
	case aiAgentClusterTaskSendContext:
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			if payload.Proactive {
				reply(agent.SendProactiveContext(payload.Text, string(payload.Source)))
			} else {
				reply(agent.SendContext(payload.Text, string(payload.Source)))
			}
		}
	case aiAgentClusterTaskParticipantChat:
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			reply(agent.HandleParticipantChatMessage(payload.UserId, payload.UserName, payload.Text))
		}
	case aiAgentClusterTaskSetLearner:
		if agent, ok := m.hostedAgent(payload.RoomId); ok {
			agent.SetLearner(aiAgentLearnerIdentity(m.aiAgentParticipantInfo(payload.RoomId, payload.UserId)))
		}
	case aiAgentClusterTaskCancelStopTimer:
		m.cancelStopTimer(payload.RoomId)
	case aiAgentClusterTaskWhiteboardScene:
		agent, ok := m.hostedAgent(payload.RoomId)
		if !ok {
			return
		}
		scene := new(plugnmeet.AIAgentWhiteboardSceneMsg)
		if err := proto.Unmarshal(payload.ProtoPayload, scene); err != nil {
			log.WithError(err).Error("failed to unmarshal the relayed whiteboard scene")
			return
		}
		if err := agent.HandleWhiteboardScene(scene); err != nil {
			log.WithError(err).Debug("failed to hand the relayed whiteboard scene to the ai agent")
		}
	case aiAgentClusterTaskToolResult:
		res := new(plugnmeet.AIAgentToolResultMsg)
		if err := proto.Unmarshal(payload.ProtoPayload, res); err != nil {
			log.WithError(err).Error("failed to unmarshal the relayed tool result")
			return
		}
		if pending := m.takePendingClientCall(res.GetCallId(), payload.RoomId, payload.UserId); pending != nil {
			pending.ch <- res
		}
	default:
		log.Warn("unknown ai agent cluster task")
	}
}

// publishClusterTask relays a task to the nodes hosting the room, if any.
func (m *AIAgentModel) publishClusterTask(task *aiAgentClusterTaskPayload) error {
	encoded, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return m.publishCluster(insights.AIAgentNatsChannel, encoded)
}

// requestClusterTask relays a task and waits for the answer of the hosting node.
func (m *AIAgentModel) requestClusterTask(task *aiAgentClusterTaskPayload, timeout time.Duration) error {
	encoded, err := json.Marshal(task)
	if err != nil {
		return err
	}

	raw, err := m.requestCluster(insights.AIAgentNatsChannel, encoded, timeout)
	if err != nil {
		if errors.Is(err, nats.ErrTimeout) || errors.Is(err, nats.ErrNoResponders) {
			// no node answered, so nothing hosts this room's agent anymore
			return config.ErrAIAgentNotRunning
		}
		return err
	}

	res := new(AgentTaskResponse)
	if err := json.Unmarshal(raw, res); err != nil {
		return fmt.Errorf("failed to parse the ai agent cluster reply: %w", err)
	}
	if !res.Status {
		if res.Msg == "" {
			return config.ErrAIAgentNotRunning
		}
		return errors.New(res.Msg)
	}
	return nil
}

// publishCluster sends a message to a subject.
func (m *AIAgentModel) publishCluster(subject string, data []byte) error {
	if m.clusterPublish != nil {
		return m.clusterPublish(subject, data)
	}
	if m.natsConn == nil {
		// unit tests run without a NATS server, so there is no cluster to relay to
		return nil
	}
	return m.natsConn.Publish(subject, data)
}

// requestCluster sends a message and waits for one answer.
func (m *AIAgentModel) requestCluster(subject string, data []byte, timeout time.Duration) ([]byte, error) {
	if m.clusterRequest != nil {
		return m.clusterRequest(subject, data, timeout)
	}
	if m.natsConn == nil {
		return nil, nats.ErrNoResponders
	}
	msg, err := m.natsConn.Request(subject, data, timeout)
	if err != nil {
		return nil, err
	}
	return msg.Data, nil
}

// cancelClusterStopTimer drops the local empty-room grace timer and lets the
// hosting node drop its own.
func (m *AIAgentModel) cancelClusterStopTimer(roomId string) {
	m.cancelStopTimer(roomId)
	if err := m.publishClusterTask(&aiAgentClusterTaskPayload{
		Task:   aiAgentClusterTaskCancelStopTimer,
		RoomId: roomId,
	}); err != nil {
		m.logger.WithError(err).WithField("roomId", roomId).Debug("could not broadcast the stop timer cancellation")
	}
}
