package insights

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

// activeParticipant holds the resources for a participant currently being processed.
type activeParticipant struct {
	transcoder *media.Transcoder
	cancel     context.CancelFunc
	identity   string
}

// roomAgent now manages tasks for a SINGLE service within a single room.
type roomAgent struct {
	ctx          context.Context
	cancel       context.CancelFunc
	conf         *config.AppConfig
	logger       *logrus.Entry
	room         *lksdk.Room
	lock         sync.RWMutex
	participants map[string]*activeParticipant
	pendingTasks map[string][]byte // key is userId
	task         insights.Task     // The single task this agent is responsible for.
	serviceName  string
}

func newRoomAgent(ctx context.Context, conf *config.AppConfig, logger *logrus.Entry, roomName, serviceName string, serviceConfig *config.ServiceConfig) (*roomAgent, error) {
	ctx, cancel := context.WithCancel(ctx)
	log := logger.WithFields(logrus.Fields{"room": roomName, "service": serviceName})

	// Create a single task for this agent's one and only service.
	task, err := NewTask(serviceName, serviceConfig, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create task for service '%s': %w", serviceName, err)
	}

	agent := &roomAgent{
		ctx:          ctx,
		cancel:       cancel,
		conf:         conf,
		logger:       log,
		participants: make(map[string]*activeParticipant),
		pendingTasks: make(map[string][]byte),
		serviceName:  serviceName,
		task:         task,
	}

	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   roomName,
		UserId:   fmt.Sprintf("insights-%s-%s", serviceName, uuid.NewString()),
		IsAdmin:  true,
		IsHidden: true,
	}
	token, err := auth.GenerateLivekitAccessToken(agent.conf.LivekitInfo.ApiKey, agent.conf.LivekitInfo.Secret, time.Minute*5, c)
	if err != nil {
		return nil, err
	}

	room := lksdk.NewRoom(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackPublished:    agent.onTrackPublished,
			OnTrackSubscribed:   agent.onTrackSubscribed,
			OnTrackUnsubscribed: agent.onTrackUnsubscribed,
		},
		OnDisconnected: agent.onDisconnected,
	})

	err = room.JoinWithToken(agent.conf.LivekitInfo.Host, token, lksdk.WithAutoSubscribe(false))
	if err != nil {
		cancel()
		return nil, err
	}

	agent.room = room
	return agent, nil
}

// ActivateTaskForUser queues a task for a user for this agent's specific service.
func (a *roomAgent) ActivateTaskForUser(userId string, options []byte) error {
	a.lock.Lock()
	if _, ok := a.pendingTasks[userId]; ok {
		a.lock.Unlock()
		a.logger.Infof("task is already pending for participant %s", userId)
		return nil
	}
	a.pendingTasks[userId] = options
	a.lock.Unlock()

	a.logger.Infof("queued task for participant %s", userId)

	// Check if track already exists.
	for _, p := range a.room.GetRemoteParticipants() {
		if p.Identity() == userId {
			for _, pub := range p.TrackPublications() {
				if pub.Kind() == lksdk.TrackKindAudio {
					return pub.(*lksdk.RemoteTrackPublication).SetSubscribed(true)
				}
			}
		}
	}
	return nil
}

// EndTasksForUser stops the task for a specific user.
func (a *roomAgent) EndTasksForUser(userId string) {
	a.lock.Lock()
	delete(a.pendingTasks, userId)
	participant, ok := a.participants[userId]
	delete(a.participants, userId)
	a.lock.Unlock()

	if ok {
		participant.cancel()
		a.logger.WithField("userId", userId).Infoln("stopped insights task for participant")
	}
}

// onTrackPublished checks if a task is pending for this user.
func (a *roomAgent) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if publication.Kind() != lksdk.TrackKindAudio {
		return
	}
	a.lock.RLock()
	_, ok := a.pendingTasks[rp.Identity()]
	a.lock.RUnlock()

	if ok {
		_ = publication.SetSubscribed(true)
	}
}

// onTrackSubscribed creates the media pipeline and runs the agent's single task.
func (a *roomAgent) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return
	}
	a.lock.Lock()
	defer a.lock.Unlock()

	options, ok := a.pendingTasks[rp.Identity()]
	if !ok {
		return
	}

	ctx, cancel := context.WithCancel(a.ctx)
	transcoder, err := media.NewTranscoder(ctx, track)
	if err != nil {
		a.logger.WithError(err).Error("failed to create transcoder")
		cancel()
		return
	}

	a.participants[rp.Identity()] = &activeParticipant{
		transcoder: transcoder,
		cancel:     cancel,
		identity:   rp.Identity(),
	}

	// Launch the agent's single, pre-created task.
	go func() {
		err := a.task.Run(ctx, transcoder.AudioStream(), a.room.Name(), rp.Identity(), options)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.logger.WithError(err).Errorf("insights task %s failed", a.serviceName)
		}
	}()

	a.logger.Infof("activated task for participant %s", rp.Identity())
	delete(a.pendingTasks, rp.Identity())
}

// onTrackUnsubscribed cleans up resources for a user.
func (a *roomAgent) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	a.EndTasksForUser(rp.Identity())
}

// Shutdown gracefully closes the agent.
func (a *roomAgent) Shutdown() {
	a.logger.Infoln("shutting down room agent")
	a.cancel()
	if a.room != nil {
		a.room.Disconnect()
	}
}

// onDisconnected is a final cleanup step.
func (a *roomAgent) onDisconnected() {
	a.logger.Infoln("agent disconnected from room")
	a.cancel()
}
