package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/pion/webrtc/v4"
	"github.com/sirupsen/logrus"

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
	trackID    string
}

// RoomAgent is a LiveKit client that acts on behalf of a specific insights service for a single room.
// It can either subscribe to specific user tracks or all tracks in the room,
// process the audio, and forward it to a designated insights.Task.
type RoomAgent struct {
	Ctx             context.Context
	cancel          context.CancelFunc
	conf            *config.AppConfig
	natsConn        *nats.Conn
	logger          *logrus.Entry
	Room            *lksdk.Room
	lock            sync.RWMutex
	activePipelines map[string]*activePipeline
	allowedUsers    map[string]bool   // Admin-defined permissions
	activeUserTasks map[string][]byte // User-driven state
	task            insights.Task     // The single task this agent is responsible for.
	redisService    *redisservice.RedisService
	natsService     *natsservice.NatsService
	payload         *insights.InsightsTaskPayload
	derivedE2EEKey  []byte // Stores the derived E2EE key for the room

	synthesisTask *TranscriptionSynthesisTask

	wg        sync.WaitGroup // Tracks running task goroutines.
	closeOnce sync.Once      // Ensures shutdown runs exactly once.
}

type RoomAgentArgs struct {
	Ctx             context.Context
	AppConf         *config.AppConfig
	NatsConn        *nats.Conn
	JS              jetstream.JetStream
	ServiceConfig   *config.ServiceConfig
	ProviderAccount *config.ProviderAccount
	NatsService     *natsservice.NatsService
	RedisService    *redisservice.RedisService
	Logger          *logrus.Entry
	Payload         *insights.InsightsTaskPayload
}

// NewRoomAgent creates and connects a new RoomAgent to a LiveKit room.
func NewRoomAgent(args *RoomAgentArgs) (*RoomAgent, error) {
	ctx, cancel := context.WithCancel(args.Ctx)
	log := args.Logger.WithFields(logrus.Fields{
		"service":     "room-agent",
		"roomId":      args.Payload.RoomId,
		"serviceType": args.Payload.ServiceType,
		"providerId":  args.ProviderAccount.ID,
		"serviceId":   args.ServiceConfig.ID,
	})

	// Create a single task for this agent's one and only service.
	taskArgs := &TaskArgs{
		Ctx:             ctx,
		ServiceType:     args.Payload.ServiceType,
		AppConf:         args.AppConf,
		NatsConn:        args.NatsConn,
		JS:              args.JS,
		ServiceConfig:   args.ServiceConfig,
		ProviderAccount: args.ProviderAccount,
		NatsService:     args.NatsService,
		RedisService:    args.RedisService,
		Logger:          log,
	}
	task, err := NewTask(taskArgs)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create task for service '%s': %w", args.Payload.ServiceType, err)
	}

	agent := &RoomAgent{
		Ctx:             ctx,
		cancel:          cancel,
		conf:            args.AppConf,
		natsConn:        args.NatsConn,
		logger:          log,
		activePipelines: make(map[string]*activePipeline),
		allowedUsers:    make(map[string]bool),
		activeUserTasks: make(map[string][]byte),
		task:            task,
		payload:         args.Payload,
		natsService:     args.NatsService,
		redisService:    args.RedisService,
	}

	// Derive E2EE key once if provided
	if args.Payload.RoomE2EEKey != nil && *args.Payload.RoomE2EEKey != "" {
		derivedKey, err := lksdk.DeriveKeyFromString(*args.Payload.RoomE2EEKey)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to derive E2EE key: %w", err)
		}
		agent.derivedE2EEKey = derivedKey
	}

	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   args.Payload.RoomId,
		UserId:   fmt.Sprintf("%s%s-%s", config.AgentUserUserIdPrefix, args.Payload.ServiceType, uuid.NewString()),
		IsAdmin:  true,
		IsHidden: args.Payload.HiddenAgent,
	}
	if args.Payload.AgentName != nil && *args.Payload.AgentName != "" {
		c.Name = *args.Payload.AgentName
	}

	// token validity can be short to 5~10 minutes as SDK will renew it periodically
	token, err := auth.GenerateLivekitAccessToken(agent.conf.LivekitInfo.ApiKey, agent.conf.LivekitInfo.Secret, time.Minute*5, c)
	if err != nil {
		cancel()
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
	log.Infof("Successfully connected with room %s", args.Payload.RoomId)

	// Start the synthesis task if enabled
	if args.Payload.EnabledTranscriptionTransSynthesis && len(args.Payload.AllowedTransLangs) > 0 {
		err := agent.startSynthesisTask()
		if err != nil {
			log.WithError(err).Error("failed to start synthesis task")
			// We don't fail the whole agent, just log the error
		}
	}

	return agent, nil
}

// startSynthesisTask initializes and runs the text-to-speech synthesis task if enabled.
func (a *RoomAgent) startSynthesisTask() error {
	// 1. Get the configuration for the speech-synthesis service.
	synthAccount, synthServiceConfig, err := a.conf.Insights.GetProviderAccountForService(insights.ServiceTypeSpeechSynthesis)
	if err != nil {
		return fmt.Errorf("failed to get provider account for speech-synthesis: %w", err)
	}

	// 2. Create a new provider instance specifically for synthesis.
	args := &ProviderArgs{
		Ctx:             a.Ctx,
		ProviderType:    synthServiceConfig.Provider,
		ProviderAccount: synthAccount,
		ServiceConfig:   synthServiceConfig,
		RDS:             a.redisService.GetRedisClient(),
		Logger:          a.logger,
	}
	synthProvider, err := NewProvider(args)
	if err != nil {
		return fmt.Errorf("failed to create provider for speech-synthesis: %w", err)
	}

	// 3. Create the new synthesis task.
	a.synthesisTask = NewTranscriptionSynthesisTask(a.Ctx, a.conf, a.natsConn, a.logger, synthProvider, synthServiceConfig, a.redisService, a.natsService, a.Room.Name(), a.payload.AllowedTransLangs, a.payload.RoomE2EEKey)

	// 4. Run the task in a goroutine.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.synthesisTask.Run()
	}()
	a.logger.Info("synthesis task started")

	return nil
}

// GetServiceType returns the specific insights service this agent is responsible for.
func (a *RoomAgent) GetServiceType() insights.ServiceType {
	return a.payload.ServiceType
}

// UpdateAllowedUsers sets the list of users who are permitted to start tasks with this agent.
// It also reconciles the state by stopping tasks for any users who are no longer allowed.
func (a *RoomAgent) UpdateAllowedUsers(allowed map[string]bool) {
	if a.payload.CaptureAllParticipantsTracks {
		a.logger.Warn("UpdateAllowedUsers called while in room-wide capture mode, ignoring.")
		return
	}

	a.lock.Lock()
	defer a.lock.Unlock()

	copiedAllowedUsers := make(map[string]bool, len(allowed))
	for userId, isAllowed := range allowed {
		copiedAllowedUsers[userId] = isAllowed
	}
	a.allowedUsers = copiedAllowedUsers

	// Reconciliation: check if any currently active users need to be removed.
	for userId := range a.activeUserTasks {
		if _, isAllowed := a.allowedUsers[userId]; !isAllowed {
			// This user is no longer allowed, stop their task.
			// We call the internal method to avoid a deadlock.
			a.endTasksForUser(userId)
		}
	}
}

// ActivateRoomWideTask enables the agent to subscribe to all current and future audio tracks in the room.
func (a *RoomAgent) ActivateRoomWideTask() {
	if !a.payload.CaptureAllParticipantsTracks {
		a.logger.Error("ActivateRoomWideTask called but agent is not in room-wide capture mode.")
		return
	}
	a.logger.Info("activating room-wide track capture")

	// Subscribe to all existing participants' microphone tracks only.
	for _, p := range a.Room.GetRemoteParticipants() {
		if a.isSystemAgent(p.Identity()) {
			continue
		}
		for _, pub := range p.TrackPublications() {
			rt, ok := pub.(*lksdk.RemoteTrackPublication)
			if !ok || !a.isMicrophoneOpusTrack(rt) {
				continue
			}
			if err := rt.SetSubscribed(true); err != nil {
				a.logger.WithError(err).WithField("userId", p.Identity()).Warn("failed to subscribe to microphone track")
			}
		}
	}
}

// ActivateTaskForUser enables the agent to subscribe to a specific user's audio track.
// It first checks if the user is in the allowed list.
func (a *RoomAgent) ActivateTaskForUser(userId string, options []byte) error {
	if a.payload.CaptureAllParticipantsTracks {
		return fmt.Errorf("cannot activate task for a single user while in room-wide capture mode")
	}

	a.lock.Lock()

	// An empty allowedUsers map means "no restriction" (open to all users in the room).
	// A non-empty map is an explicit allow-list: only listed users may start tasks.
	if len(a.allowedUsers) > 0 {
		if _, isAllowed := a.allowedUsers[userId]; !isAllowed {
			a.lock.Unlock()
			return fmt.Errorf("user %s is not allowed to perform this task", userId)
		}
	}

	if _, ok := a.activeUserTasks[userId]; ok {
		a.logger.Infof("task is already active for participant %s", userId)
		a.lock.Unlock()
		return nil
	}

	a.activeUserTasks[userId] = copyBytes(options)
	a.lock.Unlock()

	a.logger.Infof("activated task for participant %s", userId)

	// Attempt to subscribe immediately if the microphone track is already available.
	for _, p := range a.Room.GetRemoteParticipants() {
		if a.isSystemAgent(p.Identity()) {
			continue
		}
		if p.Identity() != userId {
			continue
		}
		for _, pub := range p.TrackPublications() {
			rt, ok := pub.(*lksdk.RemoteTrackPublication)
			if !ok || !a.isMicrophoneOpusTrack(rt) {
				continue
			}

			if err := rt.SetSubscribed(true); err != nil {
				a.lock.Lock()
				delete(a.activeUserTasks, userId)
				a.lock.Unlock()
				return err
			}
			return nil
		}
	}
	return nil
}

// GetUserTaskOptions returns the options for a user's active task and a boolean indicating if a task is active.
func (a *RoomAgent) GetUserTaskOptions(userId string) (insights.ServiceType, []byte, bool) {
	a.lock.RLock()
	defer a.lock.RUnlock()

	options, ok := a.activeUserTasks[userId]
	if !ok {
		return a.payload.ServiceType, nil, false
	}

	return a.payload.ServiceType, copyBytes(options), true
}

// EndTasksForUser stops all processing pipelines for a specific user.
func (a *RoomAgent) EndTasksForUser(userId string) {
	a.lock.Lock()
	a.endTasksForUser(userId)
	a.lock.Unlock()
}

// endTasksForUser is the internal, non-locking version of EndTasksForUser.
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
						if err := rt.SetSubscribed(false); err != nil {
							a.logger.WithError(err).WithField("userId", uId).Warn("failed to unsubscribe microphone track")
						}
					}
				}
			}
		}(userId)
	}
}

// onTrackPublished is a LiveKit callback that triggers when a remote participant publishes a track.
func (a *RoomAgent) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// We only transcribe the microphone track; ignore screen-share or other audio sources.
	if !a.isMicrophoneOpusTrack(publication) {
		return
	}

	log := a.logger.WithFields(logrus.Fields{
		"userId": rp.Identity(),
		"kind":   publication.Kind(),
		"name":   publication.Name(),
	})
	log.Infoln("onTrackPublished fired")

	if a.isSystemAgent(rp.Identity()) {
		log.Infoln("ignoring track from system agent")
		return
	}

	if a.payload.CaptureAllParticipantsTracks {
		if err := publication.SetSubscribed(true); err != nil {
			log.WithError(err).Warn("failed to subscribe to microphone track")
		}
		return
	}

	a.lock.RLock()
	_, ok := a.activeUserTasks[rp.Identity()]
	a.lock.RUnlock()

	if ok {
		if err := publication.SetSubscribed(true); err != nil {
			log.WithError(err).Warn("failed to subscribe to microphone track")
		}
	}
}

// onTrackSubscribed is a LiveKit callback that triggers when the agent successfully subscribes to a track.
// It creates the audio processing pipeline and starts the insights.Task.
func (a *RoomAgent) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if !a.isMicrophoneOpusTrack(publication) {
		return
	}
	a.logger.WithFields(logrus.Fields{
		"userId":     rp.Identity(),
		"kind":       publication.Kind(),
		"name":       publication.Name(),
		"encryption": publication.TrackInfo().Encryption,
	}).Infoln("onTrackSubscribed fired")

	a.lock.RLock()
	var options []byte
	if a.payload.CaptureAllParticipantsTracks {
		options = copyBytes(a.payload.Options)
	} else {
		userOptions, ok := a.activeUserTasks[rp.Identity()]
		if !ok {
			a.lock.RUnlock()
			return
		}

		options = copyBytes(userOptions)
	}
	a.lock.RUnlock()

	var decryptor lkmedia.Decryptor
	if publication.TrackInfo().GetEncryption() != livekit.Encryption_NONE {
		// Use the pre-derived key stored during agent initialization
		if a.derivedE2EEKey == nil {
			a.logger.Errorln("received an encrypted track but no derived E2EE key was available, so not continuing")
			return
		}
		var err error
		decryptor, err = lkmedia.NewGCMDecryptor(a.derivedE2EEKey, a.Room.SifTrailer())
		if err != nil {
			a.logger.WithError(err).Error("failed to create decryptor")
			return
		}
	}

	ctx, cancel := context.WithCancel(a.Ctx)
	transcoder, err := media.NewTranscoder(ctx, track, decryptor)
	if err != nil {
		a.logger.WithError(err).Error("failed to create transcoder")
		cancel()
		return
	}

	a.lock.Lock()
	trackID := publication.SID()

	// If a pipeline already exists for this participant (e.g. track re-negotiation),
	// cancel the previous one before replacing it to avoid a goroutine leak.
	if prev, exists := a.activePipelines[rp.Identity()]; exists {
		a.logger.WithField("userId", rp.Identity()).Warn("replacing existing insights pipeline for participant")
		prev.cancel()
	}

	a.activePipelines[rp.Identity()] = &activePipeline{
		transcoder: transcoder,
		cancel:     cancel,
		identity:   rp.Identity(),
		trackID:    trackID,
	}

	// Launch the agent's single, pre-created task and it's non-blocking call
	// but use go just for safety
	a.wg.Add(1)
	a.lock.Unlock()

	go func() {
		defer a.wg.Done()
		err := a.task.RunAudioStream(ctx, transcoder.AudioStream(), a.payload.RoomTableId, a.Room.Name(), rp.Identity(), options)
		if err != nil && !errors.Is(err, context.Canceled) {
			a.logger.WithError(err).Errorf("insights task %s failed", a.payload.ServiceType)
		}
	}()

	a.logger.Infof("activated task for participant %s", rp.Identity())
}

// onTrackUnsubscribed is a LiveKit callback that triggers when a track is unsubscribed.
// It cleans up the audio processing pipeline for that track.
func (a *RoomAgent) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	a.logger.WithFields(logrus.Fields{
		"userId": rp.Identity(),
		"kind":   publication.Kind(),
		"name":   publication.Name(),
	}).Infoln("onTrackUnsubscribed fired, closing related pipeline if exists")

	a.lock.Lock()
	trackID := publication.SID()

	pipeline, ok := a.activePipelines[rp.Identity()]
	if ok && pipeline.trackID == trackID {
		// Remove from the map of active pipelines.
		delete(a.activePipelines, rp.Identity())
	} else {
		ok = false
	}
	a.lock.Unlock()

	if !ok {
		// No active pipeline, nothing to do.
		return
	}

	// Stop the transcoder and associated goroutines.
	pipeline.cancel()
	a.logger.WithField("userId", rp.Identity()).Infoln("stopped insights task pipeline due to track unsubscription")
}

// Shutdown gracefully disconnects the agent from the LiveKit room and cancels all running tasks.
func (a *RoomAgent) Shutdown() {
	a.shutdownOnce()
}

// shutdownOnce performs the teardown exactly once, regardless of whether it is
// triggered by an explicit Shutdown or by an unexpected room disconnection.
func (a *RoomAgent) shutdownOnce() {
	a.closeOnce.Do(func() {
		a.logger.Infoln("received shutdown signal, cleaning up room agent")

		if a.Room != nil {
			a.Room.Disconnect()
		}

		if a.synthesisTask != nil {
			a.synthesisTask.Shutdown()
		}

		a.cancel()

		// Wait for task goroutines to finish before tearing down the room connection.
		a.wg.Wait()
	})
}

// onDisconnected is a LiveKit callback that triggers when the agent is disconnected from the room.
func (a *RoomAgent) onDisconnected() {
	a.logger.Infoln("agent disconnected from room")
	a.shutdownOnce()
}

// isSystemAgent can use to ignore user's track to consider
func (a *RoomAgent) isSystemAgent(userId string) bool {
	switch {
	case strings.HasPrefix(userId, config.TTSAgentUserIdPrefix),
		strings.HasPrefix(userId, config.AgentUserUserIdPrefix):
		return true
	}

	return false
}

// isMicrophoneOpusTrack checks if the given track publication is a microphone source and of Opus codec.
func (a *RoomAgent) isMicrophoneOpusTrack(pub lksdk.TrackPublication) bool {
	if pub == nil {
		return false
	}
	return pub.Kind() == lksdk.TrackKindAudio &&
		pub.Source() == livekit.TrackSource_MICROPHONE &&
		strings.EqualFold(pub.MimeType(), webrtc.MimeTypeOpus)
}

func copyBytes(src []byte) []byte {
	if src == nil {
		return nil
	}

	dst := make([]byte, len(src))
	copy(dst, src)

	return dst
}
