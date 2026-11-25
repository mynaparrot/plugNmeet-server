package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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

const mimeTypeAudioRed = "audio/red"

// trackSubscription is the publication method the agent needs (fakeable in tests).
type trackSubscription interface {
	SetSubscribed(subscribed bool) error
}

type activePipeline struct {
	transcoder     *media.Transcoder
	cancel         context.CancelFunc
	identity       string
	participant    *lksdk.RemoteParticipant
	participantSID string
	trackID        string
	publication    trackSubscription
	muted          chan bool
}

// ownsParticipant reports whether the pipeline was built for this participant
// object. A reconnect can replace the object, so a late event of the old one
// must not touch the pipeline of the new one.
func (p *activePipeline) ownsParticipant(rp *lksdk.RemoteParticipant) bool {
	if p == nil || rp == nil {
		return false
	}

	return p.participant == nil || p.participant == rp
}

// matchesParticipant reports whether the pipeline still serves the current
// session of the participant. A reconnect can also swap only the SID of the
// same object, so both the object and the SID have to match.
func (p *activePipeline) matchesParticipant(rp *lksdk.RemoteParticipant) bool {
	return p.ownsParticipant(rp) && matchesSID(p.participantSID, rp.SID())
}

// matchesSID reports whether a recorded SID still identifies the session. An
// empty SID on either side is unknown, not a mismatch.
func matchesSID(recorded, current string) bool {
	return recorded == "" || current == "" || recorded == current
}

// staleFor reports whether the pipeline belongs to a newer session than the
// subscription captured with participantSID. A pipeline set up concurrently for
// a newer session wins, even when the SDK mutated the same participant object
// in place, so the SID recorded at capture time is compared instead of rp.SID().
func (p *activePipeline) staleFor(rp *lksdk.RemoteParticipant, participantSID string) bool {
	return p != nil && (!p.ownsParticipant(rp) || !matchesSID(p.participantSID, participantSID))
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

	wg           sync.WaitGroup // Tracks running task goroutines.
	shuttingDown atomic.Bool    // Set before wg.Wait() so no new goroutines are registered.
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
	// AgentUserId is the LiveKit identity to join with. Callers that also
	// register the participant in NATS pass it so both agree on one id;
	// when empty a fresh identity is generated.
	AgentUserId string
}

// roomAgentIdentity returns the LiveKit identity of the agent participant.
func roomAgentIdentity(payload *insights.InsightsTaskPayload, agentUserId string) string {
	if agentUserId != "" {
		return agentUserId
	}
	return fmt.Sprintf("%s%s-%s", config.AgentUserUserIdPrefix, payload.ServiceType, uuid.NewString())
}

// NewRoomAgent creates and connects a new RoomAgent to a LiveKit room.
func NewRoomAgent(args *RoomAgentArgs) (*RoomAgent, error) {
	return newRoomAgent(args, nil)
}

// newRoomAgent connects a RoomAgent to the LiveKit room. When task is nil, a task is created from the payload's service type;
// callers that own the task (the AI agent) pass it in so their own audio pipeline runs unchanged.
func newRoomAgent(args *RoomAgentArgs, task insights.Task) (*RoomAgent, error) {
	ctx, cancel := context.WithCancel(args.Ctx)
	log := args.Logger.WithFields(logrus.Fields{
		"service":     "room-agent",
		"roomId":      args.Payload.RoomId,
		"serviceType": args.Payload.ServiceType,
		"providerId":  args.ProviderAccount.ID,
		"serviceId":   args.ServiceConfig.ID,
	})

	if task == nil {
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
		var err error
		task, err = NewTask(taskArgs)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("could not create task for service '%s': %w", args.Payload.ServiceType, err)
		}
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
		UserId:   roomAgentIdentity(args.Payload, args.AgentUserId),
		IsAdmin:  true,
		IsHidden: args.Payload.HiddenAgent,
	}
	if args.Payload.AgentName != nil && *args.Payload.AgentName != "" {
		c.Name = *args.Payload.AgentName
	}

	// token validity can be short to 5~10 minutes as SDK will renew it periodically
	token, err := auth.GenerateLivekitAccessToken(agent.conf.LivekitInfo.ApiKey, agent.conf.LivekitInfo.Secret, time.Minute*5, c, true, true, "")
	if err != nil {
		cancel()
		return nil, err
	}

	agent.Room = lksdk.NewRoom(&lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackPublished:    agent.onTrackPublished,
			OnTrackSubscribed:   agent.onTrackSubscribed,
			OnTrackUnsubscribed: agent.onTrackUnsubscribed,
			OnTrackMuted:        agent.onTrackMuted,
			OnTrackUnmuted:      agent.onTrackUnmuted,
		},
		OnParticipantConnected:    agent.onParticipantConnected,
		OnParticipantDisconnected: agent.onParticipantDisconnected,
		OnReconnected:             agent.onReconnected,
		OnDisconnected:            agent.onDisconnected,
	})

	if err := agent.Room.JoinWithToken(agent.conf.LivekitInfo.Host, token, lksdk.WithAutoSubscribe(false)); err != nil {
		cancel()
		return nil, err
	}

	log.Infof("Successfully connected with room %s", args.Payload.RoomId)

	// Room-wide callers (the AI agent) never ask for activation explicitly, so
	// subscribe participants that are already in the room. It is idempotent.
	if args.Payload.CaptureAllParticipantsTracks {
		agent.ActivateRoomWideTask()
	}

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
	if a.shuttingDown.Load() {
		a.logger.Warn("agent is shutting down, not starting synthesis task")
		return nil
	}
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
	if a.Room == nil {
		return
	}

	// Subscribe to all existing participants' microphone tracks only.
	for _, p := range a.Room.GetRemoteParticipants() {
		if a.isSystemAgent(p.Identity()) || !a.isCaptureAllowed(p.Identity()) {
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
	defer a.lock.Unlock()

	// An empty allowedUsers map means "no restriction"; otherwise it is an allow-list.
	if len(a.allowedUsers) > 0 {
		if _, isAllowed := a.allowedUsers[userId]; !isAllowed {
			return fmt.Errorf("user %s is not allowed to perform this task", userId)
		}
	}

	if _, ok := a.activeUserTasks[userId]; ok {
		a.logger.Infof("task is already active for participant %s", userId)
		return nil
	}

	a.activeUserTasks[userId] = copyBytes(options)
	a.logger.Infof("activated task for participant %s", userId)

	// Subscribe while the intent is still locked, so an end cannot interleave.
	for _, p := range a.Room.GetRemoteParticipants() {
		if a.isSystemAgent(p.Identity()) || config.PrimaryIdentityFromNative(p.Identity()) != userId {
			continue
		}
		for _, pub := range p.TrackPublications() {
			rt, ok := pub.(*lksdk.RemoteTrackPublication)
			if !ok || !a.isMicrophoneOpusTrack(rt) {
				continue
			}
			if err := rt.SetSubscribed(true); err != nil {
				delete(a.activeUserTasks, userId)
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

		go a.unsubscribePipelineTrack(userId, pipeline)
	}
}

// unsubscribePipelineTrack releases the track unless a newer pipeline or an
// in-flight activation owns it; SetSubscribed never invokes callbacks synchronously.
func (a *RoomAgent) unsubscribePipelineTrack(primaryId string, pipeline *activePipeline) {
	if pipeline == nil || pipeline.publication == nil {
		return
	}

	a.lock.Lock()
	defer a.lock.Unlock()

	if _, ok := a.activePipelines[primaryId]; ok {
		return
	}
	// ActivateTaskForUser has intent but no pipeline until the subscribe callback.
	if _, ok := a.activeUserTasks[primaryId]; ok {
		return
	}

	if err := pipeline.publication.SetSubscribed(false); err != nil {
		a.logger.WithError(err).WithField("userId", primaryId).Warn("failed to unsubscribe microphone track")
	}
}

// cleanupFailedPipeline matches by pointer so a newer activation is never killed.
func (a *RoomAgent) cleanupFailedPipeline(primaryId string, pipeline *activePipeline) {
	a.lock.Lock()
	current, ok := a.activePipelines[primaryId]
	if !ok || current != pipeline {
		a.lock.Unlock()
		return
	}
	delete(a.activePipelines, primaryId)
	perUser := !a.payload.CaptureAllParticipantsTracks
	if perUser {
		// The user must enable the service again; nothing restarts it for them.
		delete(a.activeUserTasks, primaryId)
	}
	a.lock.Unlock()

	pipeline.cancel()
	go a.unsubscribePipelineTrack(primaryId, pipeline)

	if perUser {
		a.logger.WithField("userId", primaryId).Warnln("insights task failed permanently; stopped pipeline for participant")
		return
	}
	// Room-wide capture cannot silently resume this participant's transcription.
	a.logger.WithField("userId", primaryId).Errorln("insights task failed permanently in room-wide capture mode; transcription for this participant stopped until the track is published again")
}

// stopStalePipeline cancels a pipeline built on an older session of the
// participant. A reconnect gives the participant a new object and SID, so the
// old pipeline can never receive audio again.
func (a *RoomAgent) stopStalePipeline(rp *lksdk.RemoteParticipant) {
	if rp == nil {
		return
	}
	primaryId := config.PrimaryIdentityFromNative(rp.Identity())

	a.lock.Lock()
	pipeline, ok := a.activePipelines[primaryId]
	if !ok || pipeline.matchesParticipant(rp) {
		a.lock.Unlock()
		return
	}
	delete(a.activePipelines, primaryId)
	a.lock.Unlock()

	a.logger.WithFields(logrus.Fields{
		"userId":        rp.Identity(),
		"oldSid":        pipeline.participantSID,
		"newSid":        rp.SID(),
		"objectChanged": pipeline.participant != rp,
	}).Infoln("participant reconnected with a new session, stopping previous insights pipeline")
	pipeline.cancel()
}

// shouldSubscribeParticipantLocked reports whether this agent owns the
// participant's microphone tracks.
func (a *RoomAgent) shouldSubscribeParticipantLocked(identity, primaryId string) bool {
	if a.payload.CaptureAllParticipantsTracks {
		return a.isCaptureAllowed(identity)
	}
	_, ok := a.activeUserTasks[primaryId]

	return ok
}

// onParticipantConnected is a LiveKit callback that triggers when a remote
// participant joins, including after its own reconnect.
func (a *RoomAgent) onParticipantConnected(rp *lksdk.RemoteParticipant) {
	if rp == nil || a.isSystemAgent(rp.Identity()) {
		return
	}

	a.stopStalePipeline(rp)

	primaryId := config.PrimaryIdentityFromNative(rp.Identity())
	a.lock.RLock()
	shouldSubscribe := a.shouldSubscribeParticipantLocked(rp.Identity(), primaryId)
	pipeline := a.activePipelines[primaryId]
	a.lock.RUnlock()
	if !shouldSubscribe {
		return
	}

	for _, pub := range rp.TrackPublications() {
		rt, ok := pub.(*lksdk.RemoteTrackPublication)
		if !ok || !a.isMicrophoneOpusTrack(rt) {
			continue
		}
		// A pipeline that already consumes this track is left alone.
		if rt.IsSubscribed() && pipeline.matchesParticipant(rp) && pipeline.trackID == rt.SID() {
			continue
		}
		if err := rt.SetSubscribed(true); err != nil {
			a.logger.WithError(err).WithField("userId", rp.Identity()).Warn("failed to subscribe to microphone track")
		}
	}
}

// onParticipantDisconnected is a LiveKit callback that triggers when a remote
// participant leaves. The task activation is kept, so a reconnect resumes it.
func (a *RoomAgent) onParticipantDisconnected(rp *lksdk.RemoteParticipant) {
	if rp == nil || a.isSystemAgent(rp.Identity()) {
		return
	}
	primaryId := config.PrimaryIdentityFromNative(rp.Identity())

	a.lock.Lock()
	pipeline, ok := a.activePipelines[primaryId]
	// A disconnect means the audio is gone for good, so only an event for a
	// replaced participant object may be ignored.
	if !ok || !pipeline.ownsParticipant(rp) {
		a.lock.Unlock()
		return
	}
	delete(a.activePipelines, primaryId)
	a.lock.Unlock()

	pipeline.cancel()
	a.logger.WithField("userId", rp.Identity()).Infoln("stopped insights task pipeline due to participant disconnection")
}

// onReconnected is a LiveKit callback that triggers after the agent recovered
// its own connection. The SDK re-creates remote participants, so every session
// is re-checked and re-subscribed.
func (a *RoomAgent) onReconnected() {
	a.logger.Infoln("agent reconnected to room, re-syncing participants")
	if a.Room == nil {
		return
	}

	for _, rp := range a.Room.GetRemoteParticipants() {
		a.onParticipantConnected(rp)
	}
}

// onTrackPublished is a LiveKit callback that triggers when a remote participant publishes a track.
func (a *RoomAgent) onTrackPublished(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	// We only transcribe the microphone track; ignore screen-share or other audio sources.
	if !a.isMicrophoneOpusTrack(publication) || rp == nil {
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

	// This callback usually fires first on a reconnect, so it is the earliest
	// place to stop a pipeline of a previous participant object or SID.
	a.stopStalePipeline(rp)

	primaryId := config.PrimaryIdentityFromNative(rp.Identity())
	a.lock.RLock()
	shouldSubscribe := a.shouldSubscribeParticipantLocked(rp.Identity(), primaryId)
	a.lock.RUnlock()

	if !shouldSubscribe {
		log.Infoln("participant is not part of this agent's capture, ignoring track")
		return
	}
	if err := publication.SetSubscribed(true); err != nil {
		log.WithError(err).Warn("failed to subscribe to microphone track")
	}
}

// onTrackSubscribed is a LiveKit callback that triggers when the agent successfully subscribes to a track.
// It creates the audio processing pipeline and starts the insights.Task.
func (a *RoomAgent) onTrackSubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if rp == nil || !a.isMicrophoneOpusTrack(publication) {
		return
	}

	// Capture the session before building the transcoder below, which runs
	// without the lock. The SDK mutates the participant in place on reconnect,
	// so rp.SID() can already point at a newer session once the lock is taken.
	participantSID := rp.SID()

	a.logger.WithFields(logrus.Fields{
		"userId":     rp.Identity(),
		"kind":       publication.Kind(),
		"name":       publication.Name(),
		"encryption": publication.TrackInfo().Encryption,
	}).Infoln("onTrackSubscribed fired")

	primaryId := config.PrimaryIdentityFromNative(rp.Identity())

	a.lock.RLock()
	var options []byte
	if a.payload.CaptureAllParticipantsTracks {
		options = copyBytes(a.payload.Options)
	} else {
		userOptions, ok := a.activeUserTasks[primaryId]
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

	// The task may have ended while the transcoder was built; drop the stale
	// subscription instead of installing an orphan pipeline.
	if !a.payload.CaptureAllParticipantsTracks {
		if _, ok := a.activeUserTasks[primaryId]; !ok {
			if err := publication.SetSubscribed(false); err != nil {
				a.logger.WithError(err).WithField("userId", primaryId).Warn("failed to unsubscribe microphone track")
			}
			a.lock.Unlock()
			cancel()
			return
		}
	}

	// If a pipeline already exists for this participant (e.g. track re-negotiation),
	// cancel the previous one before replacing it to avoid a goroutine leak.
	if prev, exists := a.activePipelines[primaryId]; exists {
		if prev.staleFor(rp, participantSID) {
			// A newer session owns the pipeline (it may have replaced the
			// participant object or swapped its SID in place), so drop this
			// subscription instead of cancelling a live pipeline.
			a.lock.Unlock()
			a.logger.WithFields(logrus.Fields{
				"userId":       primaryId,
				"staleSid":     participantSID,
				"currentSid":   rp.SID(),
				"pipelineSid":  prev.participantSID,
				"staleTrackId": trackID,
			}).Infoln("dropping track subscription overtaken by a newer participant session")
			if err := publication.SetSubscribed(false); err != nil {
				a.logger.WithError(err).WithField("userId", primaryId).Warn("failed to unsubscribe microphone track")
			}
			cancel()
			return
		}
		a.logger.WithField("userId", primaryId).Warn("replacing existing insights pipeline for participant")
		prev.cancel()
	}

	muted := make(chan bool, 1)
	if publication.IsMuted() {
		muted <- true
	}
	pipeline := &activePipeline{
		transcoder:     transcoder,
		cancel:         cancel,
		identity:       primaryId,
		participant:    rp,
		participantSID: participantSID,
		trackID:        trackID,
		publication:    publication,
		muted:          muted,
	}
	a.activePipelines[primaryId] = pipeline

	// Launch the agent's single, pre-created task and it's non-blocking call
	// but use go just for safety
	if a.shuttingDown.Load() {
		a.lock.Unlock()
		a.logger.Warn("agent is shutting down, not starting task for participant")
		return
	}
	a.wg.Add(1)
	a.lock.Unlock()

	go func() {
		defer a.wg.Done()

		err := a.task.RunAudioStream(ctx, transcoder.AudioStream(), muted, a.payload.RoomTableId, a.Room.Name(), primaryId, options)

		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		a.logger.WithError(err).Errorf("insights task %s failed", a.payload.ServiceType)
		if errors.Is(err, insights.ErrTranscriptionUnrecoverable) {
			a.cleanupFailedPipeline(primaryId, pipeline)
		}
	}()

	a.logger.Infof("activated task for participant %s", rp.Identity())
}

func (a *RoomAgent) onTrackMuted(pub lksdk.TrackPublication, p lksdk.Participant) {
	a.sendMuteState(pub, p, true)
}

func (a *RoomAgent) onTrackUnmuted(pub lksdk.TrackPublication, p lksdk.Participant) {
	a.sendMuteState(pub, p, false)
}

// sendMuteState hands the latest mute state to the participant's pipeline.
func (a *RoomAgent) sendMuteState(pub lksdk.TrackPublication, p lksdk.Participant, muted bool) {
	if p == nil || !a.isMicrophoneOpusTrack(pub) {
		return
	}
	primaryId := config.PrimaryIdentityFromNative(p.Identity())

	// The lock is held while sending so a replaced pipeline cannot receive a stale state.
	a.lock.RLock()
	defer a.lock.RUnlock()

	pipeline, ok := a.activePipelines[primaryId]
	if !ok || pipeline == nil || pipeline.muted == nil {
		return
	}
	// Ignore mute events of a session or track the pipeline no longer consumes.
	if pipeline.participantSID != "" && p.SID() != "" && pipeline.participantSID != p.SID() {
		return
	}
	if pipeline.trackID != "" && pub.SID() != "" && pipeline.trackID != pub.SID() {
		return
	}

	select {
	case <-pipeline.muted:
	default:
	}
	select {
	case pipeline.muted <- muted:
	default:
	}
}

// onTrackUnsubscribed is a LiveKit callback that triggers when a track is unsubscribed.
// It cleans up the audio processing pipeline for that track.
func (a *RoomAgent) onTrackUnsubscribed(track *webrtc.TrackRemote, publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
	if rp == nil || publication == nil {
		return
	}
	a.logger.WithFields(logrus.Fields{
		"userId": rp.Identity(),
		"kind":   publication.Kind(),
		"name":   publication.Name(),
	}).Infoln("onTrackUnsubscribed fired, closing related pipeline if exists")

	a.stopPipelineForTrack(rp, publication.SID())
}

// stopPipelineForTrack stops the pipeline of the participant if it still owns
// the unsubscribed track. A late unsubscribe of a replaced participant or of
// another track must not kill the pipeline that is currently running.
func (a *RoomAgent) stopPipelineForTrack(rp *lksdk.RemoteParticipant, trackID string) {
	if rp == nil {
		return
	}
	primaryId := config.PrimaryIdentityFromNative(rp.Identity())

	a.lock.Lock()
	pipeline, ok := a.activePipelines[primaryId]
	if !ok || pipeline.trackID != trackID || !pipeline.matchesParticipant(rp) {
		a.lock.Unlock()
		return
	}
	delete(a.activePipelines, primaryId)
	a.lock.Unlock()

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
	if !a.shuttingDown.CompareAndSwap(false, true) {
		// already shutting
		return
	}
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

// isCaptureAllowed reports whether a participant's audio belongs to this agent's
// task. An empty target list captures everybody in the room.
func (a *RoomAgent) isCaptureAllowed(identity string) bool {
	if len(a.payload.TargetUsers) == 0 {
		return true
	}

	return a.payload.TargetUsers[config.PrimaryIdentityFromNative(identity)]
}

// isMicrophoneOpusTrack checks if the given track publication is a microphone
// audio source encoded as Opus (directly or wrapped in RED).
// Browsers publishing with red: true send Opus inside RED, so the SFU reports
// "audio/red" while still delivering plain Opus RTP to this subscriber.
func (a *RoomAgent) isMicrophoneOpusTrack(pub lksdk.TrackPublication) bool {
	if pub == nil {
		return false
	}
	if pub.Kind() != lksdk.TrackKindAudio || pub.Source() != livekit.TrackSource_MICROPHONE {
		return false
	}
	// An empty mime type means the initial signaling has not populated it yet.
	mime := strings.TrimSpace(pub.MimeType())
	return mime == "" ||
		strings.EqualFold(mime, webrtc.MimeTypeOpus) ||
		strings.EqualFold(mime, mimeTypeAudioRed)
}

func copyBytes(src []byte) []byte {
	if src == nil {
		return nil
	}

	dst := make([]byte, len(src))
	copy(dst, src)

	return dst
}
