package insightsservice

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

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

type activeParticipant struct {
	transcoder *media.Transcoder
	cancel     context.CancelFunc
	identity   string
}

type RoomAgent struct {
	Ctx          context.Context
	cancel       context.CancelFunc
	conf         *config.AppConfig
	logger       *logrus.Entry
	Room         *lksdk.Room
	lock         sync.RWMutex
	participants map[string]*activeParticipant
	pendingTasks map[string][]byte // Simplified: key is userId, value is the options []byte
	task         insights.Task     // The single task this agent is responsible for.
	ServiceName  string
	e2eeKey      *string
}

// NewRoomAgent creates a single-purpose agent.
func NewRoomAgent(ctx context.Context, conf *config.AppConfig, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, logger *logrus.Entry, roomName, serviceName string, e2eeKey *string) (*RoomAgent, error) {
	ctx, cancel := context.WithCancel(ctx)
	log := logger.WithFields(logrus.Fields{"room": roomName, "service": serviceName})

	// Create a single task for this agent's one and only service.
	task, err := NewTask(serviceName, serviceConfig, providerAccount, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create task for service '%s': %w", serviceName, err)
	}

	agent := &RoomAgent{
		Ctx:          ctx,
		cancel:       cancel,
		conf:         conf,
		logger:       log,
		participants: make(map[string]*activeParticipant),
		pendingTasks: make(map[string][]byte),
		ServiceName:  serviceName,
		task:         task,
		e2eeKey:      e2eeKey,
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

	agent.Room = room
	return agent, nil
}

// ActivateTaskForUser queues a task for a user for this agent's specific service.
func (a *RoomAgent) ActivateTaskForUser(userId string, options []byte) error {
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
	for _, p := range a.Room.GetRemoteParticipants() {
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
func (a *RoomAgent) EndTasksForUser(userId string) {
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
func (a *RoomAgent) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
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
func (a *RoomAgent) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return
	}
	a.lock.Lock()
	defer a.lock.Unlock()

	options, ok := a.pendingTasks[rp.Identity()]
	if !ok {
		return
	}

	var decryptor lkmedia.Decryptor
	if publication.TrackInfo().GetEncryption() != livekit.Encryption_NONE {
		if a.e2eeKey == nil || *a.e2eeKey == "" {
			a.logger.Errorln("received an encrypted track but no key was provided, so not continuing")
			return
		} else {
			key, err := lksdk.DeriveKeyFromString(*a.e2eeKey)
			if err != nil {
				a.logger.WithError(err).Error("failed to derive key")
				return
			}
			decryptor, err = lkmedia.NewGCMDecryptor(key, a.Room.SifTrailer())
			if err != nil {
				a.logger.WithError(err).Error("failed to create decryptor")
				return
			}
		}
	}

	ctx, cancel := context.WithCancel(a.Ctx)
	transcoder, err := media.NewTranscoder(ctx, track, decryptor)
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
		err := a.task.RunAudioStream(ctx, transcoder.AudioStream(), a.Room.Name(), rp.Identity(), options)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.logger.WithError(err).Errorf("insights task %s failed", a.ServiceName)
		}
	}()

	a.logger.Infof("activated task for participant %s", rp.Identity())
	delete(a.pendingTasks, rp.Identity())
}

// onTrackUnsubscribed cleans up resources for a user.
func (a *RoomAgent) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	a.EndTasksForUser(rp.Identity())
}

// Shutdown gracefully closes the agent.
func (a *RoomAgent) Shutdown() {
	a.logger.Infoln("shutting down room agent")
	a.cancel()
	if a.Room != nil {
		a.Room.Disconnect()
	}
}

// onDisconnected is a final cleanup step.
func (a *RoomAgent) onDisconnected() {
	a.logger.Infoln("agent disconnected from room")
	a.cancel()
}
