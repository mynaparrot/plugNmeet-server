package models

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	clusterTestRoomId = "room-1"
	clusterTestUserId = "u1"
)

// clusterTestBus relays the ai agent cluster channel between models in process,
// so the tests run the same publish/request paths two nodes would use.
type clusterTestBus struct {
	mu        sync.Mutex
	members   []*AIAgentModel
	replies   map[string]chan []byte
	published []*aiAgentClusterTaskPayload
	// requests are the tasks a node relayed and waited an answer for.
	requests  []*aiAgentClusterTaskPayload
	requestNo int
	// timeout stands in for the answer of a node that never replies.
	timeout time.Duration
}

func newClusterTestBus(t *testing.T, members ...*AIAgentModel) *clusterTestBus {
	t.Helper()

	bus := &clusterTestBus{
		replies: make(map[string]chan []byte),
		timeout: 100 * time.Millisecond,
	}
	for _, model := range members {
		bus.members = append(bus.members, model)
		model.clusterPublish = bus.publish
		model.clusterRequest = bus.request
	}
	return bus
}

func (b *clusterTestBus) publish(subject string, data []byte) error {
	b.mu.Lock()
	if reply, ok := b.replies[subject]; ok {
		b.mu.Unlock()
		reply <- data
		return nil
	}
	b.recordLocked(subject, data)
	members := append([]*AIAgentModel(nil), b.members...)
	b.mu.Unlock()

	for _, model := range members {
		model.HandleIncomingClusterTask(&nats.Msg{Subject: subject, Data: data})
	}
	return nil
}

func (b *clusterTestBus) request(subject string, data []byte, _ time.Duration) ([]byte, error) {
	b.mu.Lock()
	b.requestNo++
	replySubject := fmt.Sprintf("_INBOX.%d", b.requestNo)
	reply := make(chan []byte, 1)
	b.replies[replySubject] = reply
	b.recordLocked(subject, data)
	if task := lastTask(b.published); task != nil {
		b.requests = append(b.requests, task)
	}
	members := append([]*AIAgentModel(nil), b.members...)
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		delete(b.replies, replySubject)
		b.mu.Unlock()
	}()

	for _, model := range members {
		model.HandleIncomingClusterTask(&nats.Msg{Subject: subject, Data: data, Reply: replySubject})
	}

	select {
	case raw := <-reply:
		return raw, nil
	case <-time.After(b.timeout):
		return nil, nats.ErrTimeout
	}
}

// publishTask sends one task over the channel, for the operations that are
// fire-and-forget.
func (b *clusterTestBus) publishTask(t *testing.T, task *aiAgentClusterTaskPayload) {
	t.Helper()

	encoded, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal task: %v", err)
	}
	if err := b.publish(insights.AIAgentNatsChannel, encoded); err != nil {
		t.Fatalf("publish task: %v", err)
	}
}

func (b *clusterTestBus) recordLocked(subject string, data []byte) {
	if subject != insights.AIAgentNatsChannel {
		return
	}
	task := new(aiAgentClusterTaskPayload)
	if err := json.Unmarshal(data, task); err != nil {
		return
	}
	b.published = append(b.published, task)
}

// lastTask returns the task that was recorded most recently, if any.
func lastTask(tasks []*aiAgentClusterTaskPayload) *aiAgentClusterTaskPayload {
	if len(tasks) == 0 {
		return nil
	}
	return tasks[len(tasks)-1]
}

// hostingNode returns a node that hosts an agent for the test room.
func hostingNode(t *testing.T) (*AIAgentModel, *stoppingAgent) {
	t.Helper()

	return hostingNodeOn(t, newAIAgentTestRedis(t))
}

// hostingNodeOn returns a hosting node that uses the given Redis, so the tests
// can share one store between the nodes of a cluster.
func hostingNodeOn(t *testing.T, rc redis.UniversalClient) (*AIAgentModel, *stoppingAgent) {
	t.Helper()

	model := newAIAgentTestModel(rc)
	model.stopTimers = make(map[string]*time.Timer)
	agent := new(stoppingAgent)
	model.activeAgentLookup = func(roomId string) (aiAgentHandle, bool) {
		if roomId != clusterTestRoomId {
			return nil, false
		}
		return agent, true
	}
	t.Cleanup(func() { model.cancelStopTimer(clusterTestRoomId) })

	return model, agent
}

// relayingNode returns a node that does not host the agent but sees the room's
// session in the shared state.
func relayingNode(t *testing.T) *AIAgentModel {
	t.Helper()

	return relayingNodeOn(t, newAIAgentTestRedis(t))
}

// relayingNodeOn returns a relaying node that uses the given Redis.
func relayingNodeOn(t *testing.T, rc redis.UniversalClient) *AIAgentModel {
	t.Helper()

	model := newAIAgentTestModel(rc)
	model.stopTimers = make(map[string]*time.Timer)
	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return nil, false
	}
	storeTestAgentState(t, model)
	t.Cleanup(func() { model.cancelStopTimer(clusterTestRoomId) })

	return model
}

// storeTestAgentState records the session state the other nodes read to know
// that an agent runs in the room.
func storeTestAgentState(t *testing.T, model *AIAgentModel) {
	t.Helper()

	raw, err := json.Marshal(&aiAgentRoomState{
		SessionId: "session-1",
		State:     plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING,
	})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := model.rds.Set(context.Background(), aiAgentTestKey(clusterTestRoomId, aiAgentKeyState), raw, 0).Err(); err != nil {
		t.Fatalf("store state: %v", err)
	}
}

func TestClusterStopReachesTheHostingNode(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	bus := newClusterTestBus(t, caller, host)

	if err := caller.Stop(context.Background(), clusterTestRoomId, "host_stopped"); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
	if len(agent.reasons) != 1 || agent.reasons[0] != "host_stopped" {
		t.Fatalf("stop reasons = %v, want one host_stopped", agent.reasons)
	}
	// the caller waits for the teardown of the hosting node, it does not fire
	// and forget, because a finalize here depends on that teardown
	if len(bus.requests) != 1 || bus.requests[0].Task != aiAgentClusterTaskStop || bus.requests[0].Reason != "host_stopped" {
		t.Fatalf("requested tasks = %v, want one stop that waits for the answer", bus.requests)
	}
}

func TestClusterStopStaysIdempotentWithoutAnAnsweredStop(t *testing.T) {
	caller := relayingNode(t)
	// a second node that does not host the agent either, so nobody answers
	other := newAIAgentTestModel(newAIAgentTestRedis(t))
	other.activeAgentLookup = func(string) (aiAgentHandle, bool) { return nil, false }
	newClusterTestBus(t, caller, other)

	if err := caller.Stop(context.Background(), clusterTestRoomId, "host_stopped"); err != nil {
		t.Fatalf("Stop() = %v, want nil for an agent that already stopped", err)
	}
}

func TestClusterStopIsNotRelayedWithoutASession(t *testing.T) {
	node := newAIAgentTestModel(newAIAgentTestRedis(t))
	node.activeAgentLookup = func(string) (aiAgentHandle, bool) { return nil, false }
	bus := newClusterTestBus(t, node)

	if err := node.Stop(context.Background(), clusterTestRoomId, "host_stopped"); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
	if len(bus.published) != 0 {
		t.Fatalf("published tasks = %d, want none without a session", len(bus.published))
	}
}

func TestClusterEmptyRoomStopCancelsTheHostingStopTimer(t *testing.T) {
	host, agent := hostingNode(t)
	host.startStopTimer(clusterTestRoomId)

	caller := relayingNode(t)
	closed := 0
	caller.closeBreakoutRoom = func(string, string) { closed++ }
	newClusterTestBus(t, caller, host)

	caller.stopEmptyRoom(clusterTestRoomId)

	if len(agent.reasons) != 1 || agent.reasons[0] != aiAgentStopReasonEmptyRoom {
		t.Fatalf("stop reasons = %v, want one %q", agent.reasons, aiAgentStopReasonEmptyRoom)
	}
	if len(host.stopTimers) != 0 {
		t.Errorf("hosting node timers = %d, want the grace timer cancelled", len(host.stopTimers))
	}
	// the hosting node closes the room after the stop, so the relaying node must not
	if closed != 0 {
		t.Errorf("closed rooms = %d, want none on the relaying node", closed)
	}
}

func TestClusterPauseAndResumeReachTheHostingNode(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	newClusterTestBus(t, caller, host)

	if err := caller.Pause(context.Background(), clusterTestRoomId); err != nil {
		t.Fatalf("Pause() = %v, want nil", err)
	}
	if err := caller.Resume(context.Background(), clusterTestRoomId); err != nil {
		t.Fatalf("Resume() = %v, want nil", err)
	}
	if agent.pauses != 1 || agent.resumes != 1 {
		t.Fatalf("pauses = %d, resumes = %d, want one of each", agent.pauses, agent.resumes)
	}

	// the hosting node answers the caller with the failure of its own agent
	agent.err = fmt.Errorf("provider session is gone")
	if err := caller.Pause(context.Background(), clusterTestRoomId); err == nil || !strings.Contains(err.Error(), "provider session is gone") {
		t.Fatalf("Pause() = %v, want the error of the hosting node", err)
	}
}

func TestClusterStatusReflectsThePauseOfAnotherNode(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	host, agent := hostingNodeOn(t, rc)
	caller := relayingNodeOn(t, rc)
	newClusterTestBus(t, caller, host)

	if res, err := caller.Status(context.Background(), clusterTestRoomId); err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	} else if got := res.GetState().GetState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING {
		t.Fatalf("state = %v, want %v", got, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING)
	}

	if err := caller.Pause(context.Background(), clusterTestRoomId); err != nil {
		t.Fatalf("Pause() = %v, want nil", err)
	}
	if agent.pauses != 1 {
		t.Fatalf("pauses = %d, want one on the hosting node", agent.pauses)
	}
	// the caller does not host the agent, so it reads the shared state
	res, err := caller.Status(context.Background(), clusterTestRoomId)
	if err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	}
	if got := res.GetState().GetState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_PAUSED {
		t.Fatalf("state = %v, want %v after the pause", got, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_PAUSED)
	}

	if err := caller.Resume(context.Background(), clusterTestRoomId); err != nil {
		t.Fatalf("Resume() = %v, want nil", err)
	}
	if agent.resumes != 1 {
		t.Fatalf("resumes = %d, want one on the hosting node", agent.resumes)
	}
	res, err = caller.Status(context.Background(), clusterTestRoomId)
	if err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	}
	if got := res.GetState().GetState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING {
		t.Fatalf("state = %v, want %v after the resume", got, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING)
	}
}

func TestClusterStatusKeepsTheStateWhenPauseFails(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	host, agent := hostingNodeOn(t, rc)
	caller := relayingNodeOn(t, rc)
	newClusterTestBus(t, caller, host)

	agent.err = fmt.Errorf("provider session is gone")
	if err := caller.Pause(context.Background(), clusterTestRoomId); err == nil {
		t.Fatal("Pause() = nil, want the error of the hosting node")
	}

	res, err := caller.Status(context.Background(), clusterTestRoomId)
	if err != nil {
		t.Fatalf("Status() = %v, want nil", err)
	}
	if got := res.GetState().GetState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING {
		t.Fatalf("state = %v, want %v after a failed pause", got, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING)
	}
}

func TestClusterSessionControlWithoutAHostingNode(t *testing.T) {
	caller := relayingNode(t)
	// a second node that does not host the agent either, so nobody answers
	other := newAIAgentTestModel(newAIAgentTestRedis(t))
	other.activeAgentLookup = func(string) (aiAgentHandle, bool) { return nil, false }
	newClusterTestBus(t, caller, other)

	if err := caller.Pause(context.Background(), clusterTestRoomId); err != config.ErrAIAgentNotRunning {
		t.Errorf("Pause() = %v, want %v", err, config.ErrAIAgentNotRunning)
	}
	if err := caller.Resume(context.Background(), clusterTestRoomId); err != config.ErrAIAgentNotRunning {
		t.Errorf("Resume() = %v, want %v", err, config.ErrAIAgentNotRunning)
	}
	if err := caller.ForwardParticipantChatMessage(context.Background(), clusterTestRoomId, clusterTestUserId, "Sam", "hello"); err != config.ErrAIAgentNotRunning {
		t.Errorf("ForwardParticipantChatMessage() = %v, want %v", err, config.ErrAIAgentNotRunning)
	}
}

func TestClusterSendContextKeepsTheProactiveSource(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	newClusterTestBus(t, caller, host)

	if err := caller.SendContext(context.Background(), clusterTestRoomId, &plugnmeet.AIAgentSendContextReq{Text: "five minutes left", Source: proto.String(string(aiAgentContextSourceCountdownTimer))}); err != nil {
		t.Fatalf("SendContext() = %v, want nil", err)
	}
	if len(agent.notes) != 1 || agent.notes[0].source != string(aiAgentContextSourceCountdownTimer) {
		t.Fatalf("proactive notes = %v, want one countdown_timer note", agent.notes)
	}

	if err := caller.SendContext(context.Background(), clusterTestRoomId, &plugnmeet.AIAgentSendContextReq{Text: "the host shared a file", Source: proto.String("host")}); err != nil {
		t.Fatalf("SendContext() = %v, want nil", err)
	}
	if len(agent.contexts) != 1 || agent.contexts[0].text != "the host shared a file" {
		t.Fatalf("contexts = %v, want one host note", agent.contexts)
	}

	// an empty injection is refused before it is relayed
	if err := caller.SendContext(context.Background(), clusterTestRoomId, &plugnmeet.AIAgentSendContextReq{Text: "   "}); err != config.ErrAIAgentInvalidRequest {
		t.Errorf("SendContext() = %v, want %v", err, config.ErrAIAgentInvalidRequest)
	}
}

func TestClusterParticipantChatReachesTheHostingNode(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	newClusterTestBus(t, caller, host)

	if err := caller.ForwardParticipantChatMessage(context.Background(), clusterTestRoomId, clusterTestUserId, "Sam", "what is a derivative?"); err != nil {
		t.Fatalf("ForwardParticipantChatMessage() = %v, want nil", err)
	}
	if len(agent.chats) != 1 || agent.chats[0].text != "what is a derivative?" || agent.chats[0].userName != "Sam" {
		t.Fatalf("chats = %v, want the participant message", agent.chats)
	}
}

func TestClusterToolResultResolvesThePendingCallOfTheHostingNode(t *testing.T) {
	host, _ := hostingNode(t)
	pending := make(chan *plugnmeet.AIAgentToolResultMsg, 1)
	host.pendingClientMu.Lock()
	host.pendingClientCalls = map[string]*pendingClientToolCall{
		"call-1": {roomID: clusterTestRoomId, targetUserID: clusterTestUserId, ch: pending},
	}
	host.pendingClientMu.Unlock()

	caller := relayingNode(t)
	newClusterTestBus(t, caller, host)

	caller.HandleClientToolResult(context.Background(), clusterTestRoomId, clusterTestUserId, &plugnmeet.AIAgentToolResultMsg{
		CallId: "call-1", Ok: true, ResultJson: "{}",
	})

	select {
	case res := <-pending:
		if res.GetCallId() != "call-1" {
			t.Fatalf("resolved call = %q, want call-1", res.GetCallId())
		}
	case <-time.After(time.Second):
		t.Fatal("the hosting node never resolved the relayed tool result")
	}
}

func TestClusterWhiteboardSceneAndLearnerReachTheHostingNode(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	bus := newClusterTestBus(t, caller, host)

	scene := &plugnmeet.AIAgentWhiteboardSceneMsg{RoomId: clusterTestRoomId, SceneGraphJson: `{"elements":[]}`}
	raw, err := proto.Marshal(scene)
	if err != nil {
		t.Fatalf("marshal scene: %v", err)
	}
	bus.publishTask(t, &aiAgentClusterTaskPayload{
		Task:         aiAgentClusterTaskWhiteboardScene,
		RoomId:       clusterTestRoomId,
		UserId:       clusterTestUserId,
		ProtoPayload: raw,
	})
	if len(agent.scenes) != 1 || agent.scenes[0].GetSceneGraphJson() != `{"elements":[]}` {
		t.Fatalf("scenes = %v, want the relayed snapshot", agent.scenes)
	}

	bus.publishTask(t, &aiAgentClusterTaskPayload{
		Task:   aiAgentClusterTaskSetLearner,
		RoomId: clusterTestRoomId,
		UserId: clusterTestUserId,
	})
	if len(agent.learners) != 1 || agent.learners[0].UserID != clusterTestUserId {
		t.Fatalf("learners = %v, want the user of the room", agent.learners)
	}
}

func TestClusterCancelStopTimerReachesTheHostingNode(t *testing.T) {
	host, _ := hostingNode(t)
	host.startStopTimer(clusterTestRoomId)

	caller := relayingNode(t)
	caller.startStopTimer(clusterTestRoomId)
	newClusterTestBus(t, caller, host)

	caller.cancelClusterStopTimer(clusterTestRoomId)

	if len(caller.stopTimers) != 0 || len(host.stopTimers) != 0 {
		t.Fatalf("pending timers = %d on the caller, %d on the host, want none", len(caller.stopTimers), len(host.stopTimers))
	}
}

func TestStopForRoomEndCancelsTheStopTimerOnEveryNode(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	host, agent := hostingNodeOn(t, rc)
	host.startStopTimer(clusterTestRoomId)

	caller := relayingNodeOn(t, rc)
	caller.startStopTimer(clusterTestRoomId)
	newClusterTestBus(t, caller, host)

	caller.StopForRoomEnd(context.Background(), clusterTestRoomId)

	if len(agent.reasons) != 1 || agent.reasons[0] != "room_ended" {
		t.Fatalf("stop reasons = %v, want one %q", agent.reasons, "room_ended")
	}
	if len(caller.stopTimers) != 0 || len(host.stopTimers) != 0 {
		t.Fatalf("pending timers = %d on the caller, %d on the host, want none", len(caller.stopTimers), len(host.stopTimers))
	}
}

func TestClusterTaskIsIgnoredByANodeWithoutTheRoom(t *testing.T) {
	host, agent := hostingNode(t)
	caller := relayingNode(t)
	bus := newClusterTestBus(t, caller, host)

	bus.publishTask(t, &aiAgentClusterTaskPayload{Task: aiAgentClusterTaskPause, RoomId: "another-room"})
	bus.publishTask(t, &aiAgentClusterTaskPayload{Task: aiAgentClusterTask("unknown-task"), RoomId: clusterTestRoomId})

	if agent.pauses != 0 {
		t.Errorf("pauses = %d, want none for another room", agent.pauses)
	}
}
