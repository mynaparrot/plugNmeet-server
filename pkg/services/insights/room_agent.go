package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"

	lkLogger "github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

type activePipeline struct {
	transcoder *media.Transcoder
	cancel     context.CancelFunc
	identity   string
}

type RoomAgent struct {
	Ctx             context.Context
	cancel          context.CancelFunc
	conf            *config.AppConfig
	logger          *logrus.Entry
	Room            *lksdk.Room
	lock            sync.RWMutex
	activePipelines map[string]*activePipeline
	allowedUsers    map[string]bool   // Admin-defined permissions
	activeUserTasks map[string][]byte // User-driven state
	task            insights.Task     // The single task this agent is responsible for.
	ServiceType     insights.ServiceType
	e2eeKey         *string
}

// NewRoomAgent creates a single-purpose agent.
func NewRoomAgent(ctx context.Context, conf *config.AppConfig, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, natsService *natsservice.NatsService, redisService *redisservice.RedisService, logger *logrus.Entry, payload *insights.InsightsTaskPayload) (*RoomAgent, error) {
	ctx, cancel := context.WithCancel(ctx)
	log := logger.WithFields(logrus.Fields{
		"service":     "room-agent",
		"roomId":      payload.RoomId,
		"serviceType": payload.ServiceType,
		"providerId":  providerAccount.ID,
		"serviceId":   serviceConfig.ID,
	})

	// Create a single task for this agent's one and only service.
	task, err := NewTask(payload.ServiceType, serviceConfig, providerAccount, natsService, redisService, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create task for service '%s': %w", payload.ServiceType, err)
	}

	agent := &RoomAgent{
		Ctx:             ctx,
		cancel:          cancel,
		conf:            conf,
		logger:          log,
		activePipelines: make(map[string]*activePipeline),
		allowedUsers:    make(map[string]bool),
		activeUserTasks: make(map[string][]byte),
		ServiceType:     payload.ServiceType,
		task:            task,
		e2eeKey:         payload.RoomE2EEKey,
	}

	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   payload.RoomId,
		UserId:   fmt.Sprintf("%s%s_%s", config.AgentUserUserIdPrefix, payload.ServiceType, uuid.NewString()),
		IsAdmin:  true,
		IsHidden: payload.HiddenAgent,
	}
	if payload.AgentName != nil && *payload.AgentName != "" {
		c.Name = *payload.AgentName
	}

	// TODO: if not hidden then we'll need to display this user in the session
	// so, need to add this user to nats KV as normal user

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
	room.SetLogger(lkLogger.GetLogger())

	err = room.JoinWithToken(agent.conf.LivekitInfo.Host, token, lksdk.WithAutoSubscribe(false))
	if err != nil {
		cancel()
		return nil, err
	}

	agent.Room = room
	log.Infof("successfully connected with room %s", payload.RoomId)

	return agent, nil
}

// UpdateAllowedUsers is the new reconciliation logic.
func (a *RoomAgent) UpdateAllowedUsers(allowed map[string]bool) {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.allowedUsers = allowed

	// Reconciliation: check if any currently active users need to be removed.
	for userId := range a.activeUserTasks {
		if _, isAllowed := a.allowedUsers[userId]; !isAllowed {
			// This user is no longer allowed, stop their task.
			// We call the internal method to avoid a deadlock.
			a.endTasksForUser(userId)
		}
	}
}

// ActivateTaskForUser now checks for permission first.
func (a *RoomAgent) ActivateTaskForUser(userId string, options []byte) error {
	a.lock.Lock()

	if _, isAllowed := a.allowedUsers[userId]; !isAllowed {
		// If the allowed list is not empty and the user is not in it.
		if len(a.allowedUsers) > 0 {
			a.lock.Unlock()
			return fmt.Errorf("user %s is not allowed to perform this task", userId)
		}
	}

	if _, ok := a.activeUserTasks[userId]; ok {
		a.logger.Infof("task is already active for participant %s", userId)
		a.lock.Unlock()
		return nil
	}
	a.activeUserTasks[userId] = options
	a.lock.Unlock()

	a.logger.Infof("activated task for participant %s", userId)
	fmt.Println("a.Room.ConnectionState ====>>>> ", a.Room.ConnectionState())

	// Attempt to subscribe immediately if the track is already available.
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
	a.endTasksForUser(userId)
	a.lock.Unlock()
}

// endTasksForUser is the internal, non-locking version.
// it's assumed that the caller has hold mutex lock.
func (a *RoomAgent) endTasksForUser(userId string) {
	delete(a.activeUserTasks, userId)
	pipeline, ok := a.activePipelines[userId]
	delete(a.activePipelines, userId)

	if ok {
		pipeline.cancel()
		a.logger.WithField("userId", userId).Infoln("stopped insights task for participant")
	}
}

// onTrackPublished now checks the activeUserTasks map.
func (a *RoomAgent) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if publication.Kind() != lksdk.TrackKindAudio {
		return
	}
	a.logger.WithFields(logrus.Fields{
		"userId": rp.Identity(),
		"kind":   publication.Kind(),
		"name":   publication.Name(),
	}).Infoln("onTrackPublished fired")

	a.lock.RLock()
	_, ok := a.activeUserTasks[rp.Identity()]
	a.lock.RUnlock()

	if ok {
		_ = publication.SetSubscribed(true)
	}
}

// onTrackSubscribed now uses the activeUserTasks map and does NOT delete from it.
func (a *RoomAgent) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if track.Codec().MimeType != webrtc.MimeTypeOpus {
		return
	}
	a.logger.WithFields(logrus.Fields{
		"userId":     rp.Identity(),
		"kind":       publication.Kind(),
		"name":       publication.Name(),
		"encryption": publication.TrackInfo().Encryption,
	}).Infoln("onTrackSubscribed fired")

	a.lock.Lock()
	defer a.lock.Unlock()

	options, ok := a.activeUserTasks[rp.Identity()]
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

	a.activePipelines[rp.Identity()] = &activePipeline{
		transcoder: transcoder,
		cancel:     cancel,
		identity:   rp.Identity(),
	}

	// Launch the agent's single, pre-created task.
	go func() {
		err := a.task.RunAudioStream(ctx, transcoder.AudioStream(), a.Room.Name(), rp.Identity(), options)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.logger.WithError(err).Errorf("insights task %s failed", a.ServiceType)
		}
	}()

	a.logger.Infof("activated task for participant %s", rp.Identity())
}

// onTrackUnsubscribed cleans up the processing pipeline for a user's track,
// but preserves their "active" status in case they reconnect.
func (a *RoomAgent) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	a.lock.Lock()
	pipeline, ok := a.activePipelines[rp.Identity()]
	// Remove from the map of active pipelines.
	delete(a.activePipelines, rp.Identity())
	a.lock.Unlock()

	if !ok {
		// No active pipeline, nothing to do.
		return
	}

	// Stop the transcoder and associated goroutines.
	pipeline.cancel()
	a.logger.WithField("userId", rp.Identity()).Infoln("stopped insights task pipeline due to track unsubscription")
}

// Shutdown gracefully closes the agent.
func (a *RoomAgent) Shutdown() {
	a.logger.Infoln("received shutdown signal, disconnecting room agent")
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
