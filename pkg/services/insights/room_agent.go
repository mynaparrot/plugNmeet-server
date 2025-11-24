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
	natsService     *natsservice.NatsService
	payload         *insights.InsightsTaskPayload

	synthesisTask *TranscriptionSynthesisTask
}

// NewRoomAgent creates a single-purpose agent.
func NewRoomAgent(ctx context.Context, appConf *config.AppConfig, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, natsService *natsservice.NatsService, redisService *redisservice.RedisService, logger *logrus.Entry, payload *insights.InsightsTaskPayload) (*RoomAgent, error) {
	ctx, cancel := context.WithCancel(ctx)
	log := logger.WithFields(logrus.Fields{
		"service":     "room-agent",
		"roomId":      payload.RoomId,
		"serviceType": payload.ServiceType,
		"providerId":  providerAccount.ID,
		"serviceId":   serviceConfig.ID,
	})

	// Create a single task for this agent's one and only service.
	task, err := NewTask(payload.ServiceType, appConf, serviceConfig, providerAccount, natsService, redisService, log)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create task for service '%s': %w", payload.ServiceType, err)
	}

	agent := &RoomAgent{
		Ctx:             ctx,
		cancel:          cancel,
		conf:            appConf,
		logger:          log,
		activePipelines: make(map[string]*activePipeline),
		allowedUsers:    make(map[string]bool),
		activeUserTasks: make(map[string][]byte),
		task:            task,
		payload:         payload,
		natsService:     natsService,
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

	// Start the synthesis task if enabled
	if payload.EnabledTranscriptionTransSynthesis && len(payload.AllowedTransLangs) > 0 {
		err := agent.startSynthesisTask()
		if err != nil {
			log.WithError(err).Error("failed to start synthesis task")
			// We don't fail the whole agent, just log the error
		}
	}

	return agent, nil
}

func (a *RoomAgent) startSynthesisTask() error {
	// 1. Get the configuration for the speech-synthesis service.
	synthAccount, synthServiceConfig, err := a.conf.Insights.GetProviderAccountForService(insights.ServiceTypeSpeechSynthesis)
	if err != nil {
		return fmt.Errorf("failed to get provider account for speech-synthesis: %w", err)
	}

	// 2. Create a new provider instance specifically for synthesis.
	synthProvider, err := NewProvider(synthServiceConfig.Provider, synthAccount, synthServiceConfig, a.logger)
	if err != nil {
		return fmt.Errorf("failed to create provider for speech-synthesis: %w", err)
	}

	// 3. Create the new synthesis task.
	a.synthesisTask = NewTranscriptionSynthesisTask(a.Ctx, a.conf, a.logger, synthProvider, synthServiceConfig, a.Room.Name(), a.payload.RoomE2EEKey, a.natsService, a.payload.AllowedTransLangs)

	// 4. Run the task in a goroutine.
	go a.synthesisTask.Run()
	a.logger.Info("synthesis task started")

	return nil
}

func (a *RoomAgent) GetServiceType() insights.ServiceType {
	return a.payload.ServiceType
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

// GetUserTaskOptions safely checks if a user has an active task and returns the options.
// It returns the options and a boolean indicating if the user was found.
func (a *RoomAgent) GetUserTaskOptions(userId string) (insights.ServiceType, []byte, bool) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	options, ok := a.activeUserTasks[userId]
	return a.payload.ServiceType, options, ok
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

		go func(uId string) {
			if rp := a.Room.GetParticipantByIdentity(uId); rp != nil {
				if tp := rp.GetTrackPublication(livekit.TrackSource_MICROPHONE); tp != nil {
					if rt, ok := tp.(*lksdk.RemoteTrackPublication); ok {
						// we'll need to unsubscribe the track
						_ = rt.SetSubscribed(false)
					}
				}
			}
		}(userId)
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
		if a.payload.RoomE2EEKey == nil || *a.payload.RoomE2EEKey == "" {
			a.logger.Errorln("received an encrypted track but no key was provided, so not continuing")
			return
		} else {
			key, err := lksdk.DeriveKeyFromString(*a.payload.RoomE2EEKey)
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
			a.logger.WithError(err).Errorf("insights task %s failed", a.payload.ServiceType)
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
	if a.synthesisTask != nil {
		a.synthesisTask.Shutdown()
	}
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
