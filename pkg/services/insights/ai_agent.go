package insightsservice

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	mediasdk "github.com/livekit/media-sdk"
	lkauth "github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsmedia "github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
	avatarservice "github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/avatar"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const (
	// aiAgentTrackName is the published microphone track participants hear.
	aiAgentTrackName = "ai-agent-voice"
	// aiAgentInAudioBuffer bounds the participant audio waiting for the provider.
	// Audio older than roughly two seconds is worthless, so overflow drops the oldest chunk instead of blocking the RTP reader.
	aiAgentInAudioBuffer = 32
	// aiAgentWhiteboardToolPrefix marks the client-delegated whiteboard tools.
	aiAgentWhiteboardToolPrefix = "whiteboard_"
	// aiAgentDefaultMaxAudioMinutes mirrors the documented provider budget.
	aiAgentDefaultMaxAudioMinutes = 120
	// maxSessionTranscriptTurns bounds the transcript kept for the summary; the tail is what a handoff needs,
	// and the whole call would bloat Redis.
	maxSessionTranscriptTurns = 40
	// maxTranscriptTurnChars bounds one turn so a rambling answer cannot dominate the payload.
	maxTranscriptTurnChars = 240
	// aiAgentTelemetryTimeout bounds the flush that outlives a cancelled session.
	aiAgentTelemetryTimeout = 3 * time.Second
	// defaultAISummarizeWindow is how many unsummarized transcript turns trigger he background summary:
	// eight turns are four question-answer exchanges.
	defaultAISummarizeWindow = 8
	// aiAgentSummarizeTimeout bounds one background summary, so a slow model can never hold the session.
	aiAgentSummarizeTimeout = 5 * time.Second

	transcriptRoleParticipant = "participant"
	transcriptRoleAgent       = "agent"
	// transcriptRoleContext marks a server-side note (a poll answer, a timer) that
	// reached the session: it belongs to the record but is not a participant turn.
	transcriptRoleContext = "context"
)

// aiAgentSpeakingIdle is how long the agent may stay silent before the status falls back from SPEAKING to LISTENING.
// Providers stream audio in 20-200ms chunks, so a longer gap means the turn ended — even when no turn-complete event follows.
// A variable so tests can shorten it.
var aiAgentSpeakingIdle = 1500 * time.Millisecond

// aiAgentThinkingIdle is the safety net of the THINKING state: a turn that never
// produces audio nor a turn-complete falls back to LISTENING after it.
var aiAgentThinkingIdle = 12 * time.Second

// aiAgentPlayoutGrace is added to the remaining playout before the state is
// rechecked, so the timer lands after the last frame reached the listener.
const aiAgentPlayoutGrace = 150 * time.Millisecond

// AIAgentTransport is the room-facing messaging an AI agent needs.
// *natsservice.NatsService implements it; tests inject a recorder.
type AIAgentTransport interface {
	BroadcastAIAgentState(roomId string, msg *plugnmeet.AIAgentStateMsg) error
}

// audioPublisher is what every way of sending the agent voice implements:
// a published track, a datastream segment, or an avatar session socket.
type audioPublisher = avatarservice.AudioPublisher

// appMessagePublisher is the publisher subset that can send a LiveKit app
// message to the avatar. Only the datastream publisher implements it, which is
// what every avatar session runs on.
type appMessagePublisher interface {
	SendAppMessage(topic string, payload []byte) error
}

// sessionAudioPublisherProvider is an avatar session that streams the voice over
// its own connection instead of a LiveKit datastream.
type sessionAudioPublisherProvider interface {
	AudioPublisher() avatarservice.AudioPublisher
}

// listeningSetter is an avatar session that can be told whether the agent is
// listening to the room.
type listeningSetter interface {
	SetListening(listening bool)
}

// activeAIAgents holds the AI agents hosted by this process, keyed by room id.
// Only the leader that owns the room's LiveKit connection appears here; the
// model layer routes room-scoped control calls through it.
var activeAIAgents sync.Map

// GetActiveAIAgent returns the AI agent this process hosts for roomId.
func GetActiveAIAgent(roomId string) (*AIAgent, bool) {
	v, ok := activeAIAgents.Load(roomId)
	if !ok {
		return nil, false
	}
	agent, ok := v.(*AIAgent)
	return agent, ok && agent != nil
}

func registerActiveAIAgent(a *AIAgent) {
	activeAIAgents.Store(a.roomId, a)
}

func unregisterActiveAIAgent(a *AIAgent) {
	if v, ok := activeAIAgents.Load(a.roomId); ok && v == a {
		activeAIAgents.Delete(a.roomId)
	}
}

// aiAgentRuntimeState is the runtime state broadcast to the room, plus the
// telemetry the session summary is built from. stateMu guards all of it.
type aiAgentRuntimeState struct {
	state        plugnmeet.AIAgentRuntimeState
	speaking     bool
	tool         string
	reason       string
	inputTokens  uint32
	outputTokens uint32
	// cachedInputTokens is the cached prefix of inputTokens: the provider
	// billed it at a discounted rate, so it is priced apart.
	cachedInputTokens uint64
	// Modality split of the same tokens, so each one is priced at its own rate.
	textInputTokens        uint64
	audioInputTokens       uint64
	imageInputTokens       uint64
	cachedTextInputTokens  uint64
	cachedAudioInputTokens uint64
	cachedImageInputTokens uint64
	textOutputTokens       uint64
	audioOutputTokens      uint64
	thinkingOutputTokens   uint64
	// cacheWriteInputTokens is the input written to the prompt cache, split by
	// modality like the cached count above.
	cacheWriteInputTokens      uint64
	cacheWriteTextInputTokens  uint64
	cacheWriteAudioInputTokens uint64
	cacheWriteImageInputTokens uint64
	audioMillis                uint64
	// delegationInputTokens and delegationOutputTokens belong to the delegated
	// text model of a live session, so they never mix with the session tokens.
	delegationInputTokens  uint32
	delegationOutputTokens uint32
	// The delegated tokens carry the same modality and cache split.
	delegationCachedInputTokens          uint64
	delegationCacheWriteInputTokens      uint64
	delegationTextInputTokens            uint64
	delegationAudioInputTokens           uint64
	delegationImageInputTokens           uint64
	delegationCachedTextInputTokens      uint64
	delegationCachedAudioInputTokens     uint64
	delegationCachedImageInputTokens     uint64
	delegationCacheWriteTextInputTokens  uint64
	delegationCacheWriteAudioInputTokens uint64
	delegationCacheWriteImageInputTokens uint64
	delegationTextOutputTokens           uint64
	delegationAudioOutputTokens          uint64
	delegationThinkingOutputTokens       uint64

	turnsCount int
	toolsUsed  map[string]int
	transcript []redisservice.AISessionTranscriptTurn
	// totalTranscriptTurns counts every turn ever appended, even after the
	// transcript ring buffer dropped the oldest ones.
	totalTranscriptTurns int
	// rollingSummary is the running summary of the conversation so far and
	// summarizedTurns the turn count it covered.
	rollingSummary  string
	summarizedTurns int
	// The rolling summaries are billed apart from the session tokens.
	summaryInputTokens  uint64
	summaryOutputTokens uint64
}

// aiAgentLoop ties one provider session to its event stream so a redial can
// retire the old reader without touching the new one.
type aiAgentLoop struct {
	session insights.RealtimeSession
	events  <-chan insights.RealtimeEvent
	stop    chan struct{}
}

// AIAgent is the visible in-room voice assistant: it joins the LiveKit room as a
// participant, forwards participant audio to a realtime provider, plays the
// agent's voice back, and runs the room's MCP tools.
type AIAgent struct {
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *logrus.Entry
	roomId      string
	sessionId   string
	agentUserId string
	agentName   string
	startedAt   time.Time

	boundary  BoundaryConfig
	features  *plugnmeet.InsightsAIAgentFeatures
	guard     *BoundaryGuard
	executor  *mcp.Executor
	transport AIAgentTransport
	onStopped func(reason string)
	maxAudio  time.Duration
	// redisService persists the session telemetry; nil in tests without a store.
	redisService *redisservice.RedisService

	// estimateCost prices a telemetry snapshot, so the flush carries the live
	// cost of the session; nil disables the estimate.
	estimateCost func(t *redisservice.AIAgentSessionTelemetry) (tokenCost, avatarCost float64)

	// summarize rolls the conversation summary forward in the background; nil
	// when the service configures no summarize model.
	summarize func(ctx context.Context, history []*plugnmeet.InsightsAITextChatContent) (summary string, promptTokens, completionTokens uint32, err error)
	// summarizeWindow is how many unsummarized turns trigger summarize.
	summarizeWindow int
	summarizing     atomic.Bool
	// summarizeMu guards the group, because a turn can arrive from a chat
	// handler outside the event loop while Stop is already waiting.
	summarizeMu   sync.Mutex
	summarizeWg   sync.WaitGroup
	summarizeStop bool
	// resuming marks an agent that continues an earlier session of the room, so
	// Start hands the new provider socket the conversation so far.
	resuming bool

	roomAgent *RoomAgent
	publisher audioPublisher

	// customProvider names the host's own provider the session runs on, whose
	// usage has no platform cost; empty for the platform provider.
	customProvider string

	// The realtime avatar publishing the lip-synced video of the agent. The
	// provider bot joins the room as the agent's native twin, so the agent audio
	// reaches it as a datastream instead of a published track.
	avatarSession avatarservice.Session
	// avatarProvider names the avatar provider of the session and
	// customAvatarProvider the same name when the room brought its own key,
	// which the platform never prices. Both are empty without an avatar.
	avatarProvider       string
	customAvatarProvider string
	// maxAvatar is the avatar budget of the session, zero when unlimited.
	maxAvatar       time.Duration
	avatarTimer     *time.Timer
	avatarStartedAt time.Time
	// priorAvatarMillis is the avatar duration of the earlier session of the
	// room, so a restart continues the budget instead of resetting it.
	priorAvatarMillis uint64
	// avatarEmotion is the emotion the avatar already accepted, guarded by
	// emotionMu. It keeps the state loop from repeating the same app message.
	avatarEmotion string
	emotionMu     sync.Mutex

	inAudio chan mediasdk.PCM16Sample

	sessionMu  sync.RWMutex
	session    insights.RealtimeSession
	newSession func(ctx context.Context) (insights.RealtimeSession, error)

	loopMu sync.Mutex
	loop   *aiAgentLoop

	// speakTimer leaves SPEAKING when the agent audio stops; stateMu guards it.
	stateMu    sync.Mutex
	state      aiAgentRuntimeState
	speakTimer *time.Timer
	// pendingThinking defers THINKING until the audio already queued is heard,
	// so a filler acknowledgement finishes before the status leaves SPEAKING.
	pendingThinking bool

	learnerMu sync.Mutex
	learner   LearnerIdentity

	// lastAutoVisionAt throttles the automatic participant_edit snapshots.
	lastAutoVisionAt atomic.Int64
	// lastProactiveAt and lastChatTurnAt bound how often a burst of server notes
	// or chat messages can force a model turn.
	lastProactiveAt atomic.Int64
	lastChatTurnAt  atomic.Int64

	// redialMu guards the consecutive redial budget of a flapping provider.
	redialMu           sync.Mutex
	consecutiveRedials int
	sessionOpenedAt    time.Time

	paused atomic.Bool

	// toolTurn marks a turn that answered with a tool call instead of speech.
	// Its turn-complete is silent and must not advance the anchor cadence.
	toolTurn atomic.Bool

	sessionTimer *time.Timer
	startOnce    sync.Once
	stopOnce     sync.Once
	wg           sync.WaitGroup
	done         chan struct{}
}

// LearnerIdentity is the participant of a 1-on-1 session. It travels with the
// session so the summary artifact can still name the learner after they left.
type LearnerIdentity struct {
	UserID    string
	UserName  string
	ExUserID  string
	ExtraData string
}

// WithFallback returns the identity where every empty field of next is replaced
// by the value already known here.
func (l LearnerIdentity) WithFallback(next LearnerIdentity) LearnerIdentity {
	if next.UserID == "" {
		next.UserID = l.UserID
	}
	if next.UserName == "" {
		next.UserName = l.UserName
	}
	if next.ExUserID == "" {
		next.ExUserID = l.ExUserID
	}
	if next.ExtraData == "" {
		next.ExtraData = l.ExtraData
	}
	return next
}

// AIAgentArgs carries everything needed to connect a realtime session and a
// visible LiveKit participant for one room.
type AIAgentArgs struct {
	Ctx             context.Context
	AppConf         *config.AppConfig
	NatsConn        *nats.Conn
	JS              jetstream.JetStream
	Transport       AIAgentTransport
	NatsService     *natsservice.NatsService
	RedisService    *redisservice.RedisService
	ServiceConfig   *config.ServiceConfig
	ProviderAccount *config.ProviderAccount
	// CustomProvider names the host's own provider (BYOK/BYOP), so the session
	// never carries a platform token cost; empty for the platform provider.
	CustomProvider string
	Logger         *logrus.Entry
	Payload        *insights.InsightsTaskPayload
	Features       *plugnmeet.InsightsAIAgentFeatures
	Boundary       BoundaryConfig
	Deps           mcp.ToolDependencies
	// AgentUserId is the participant identity. Callers register it in NATS
	// before the agent joins LiveKit so browsers can resolve the audio track;
	// when empty a fresh identity is generated.
	AgentUserId string
	// Learner is the participant of the 1-on-1 session; the session telemetry
	// carries it into the summary artifact.
	Learner LearnerIdentity
	// OnStopped runs once after the agent has fully stopped.
	OnStopped func(reason string)
	// SummarizeModel refines the running conversation summary; an empty model
	// disables the background summarization.
	SummarizeModel string
	// PreviousTelemetry is the room's earlier session. A restart in the same
	// room continues its conversation and its token usage instead of wiping them.
	PreviousTelemetry *redisservice.AIAgentSessionTelemetry
	// Avatar is the realtime avatar of the session; nil when the session runs
	// without one.
	Avatar *AIAgentAvatarConfig
	// EstimateCost prices a telemetry snapshot for the live cost tracking.
	EstimateCost func(t *redisservice.AIAgentSessionTelemetry) (tokenCost, avatarCost float64)
}

// AIAgentAvatarConfig is everything needed to start the realtime avatar of a
// session. The provider bot joins the room of the agent and publishes the
// lip-synced video of AvatarID while the agent audio reaches it as a stream.
type AIAgentAvatarConfig struct {
	Provider config.ProviderType
	AvatarID string
	APIKey   string
	Endpoint string
	// AuthHeader is the optional header name that carries the API key; empty
	// keeps the provider's own default header.
	AuthHeader string
	IsCustom   bool
	MaxMinutes uint32
	// StartFunc overrides how the avatar session is started; nil uses
	// avatar.StartSession. Tests inject a fake provider through it.
	StartFunc func(ctx context.Context, cfg avatarservice.StartConfig) (avatarservice.Session, error)
}

// aiAgentCoreArgs is everything the agent needs that does not depend on a
// LiveKit connection, so the runtime loop is testable without a media server.
type aiAgentCoreArgs struct {
	Ctx          context.Context
	Payload      *insights.InsightsTaskPayload
	Features     *plugnmeet.InsightsAIAgentFeatures
	Boundary     BoundaryConfig
	Session      insights.RealtimeSession
	NewSession   func(ctx context.Context) (insights.RealtimeSession, error)
	Transport    AIAgentTransport
	RedisService *redisservice.RedisService
	Logger       *logrus.Entry
	Registry     *mcp.Registry
	// ClientRPC runs the tools delegated to a browser; nil disables them.
	ClientRPC      mcp.ClientRPCSender
	OnStopped      func(reason string)
	SessionId      string
	AgentUserId    string
	Learner        LearnerIdentity
	MaxAudioMinute uint32
	// CustomProvider names the host's provider the session runs on, empty when
	// the server's platform provider serves it.
	CustomProvider string
	// Summarize rolls the running conversation summary forward.
	Summarize func(ctx context.Context, history []*plugnmeet.InsightsAITextChatContent) (summary string, promptTokens, completionTokens uint32, err error)
	// SummarizeWindow is how many unsummarized turns trigger Summarize.
	SummarizeWindow int
	// PreviousTelemetry continues an earlier session of the same room.
	PreviousTelemetry *redisservice.AIAgentSessionTelemetry
	// Avatar configures the realtime avatar of the session; nil runs the agent
	// without one, publishing its voice as a track.
	Avatar *AIAgentAvatarConfig
	// EstimateCost prices a telemetry snapshot for the live cost tracking.
	EstimateCost func(t *redisservice.AIAgentSessionTelemetry) (tokenCost, avatarCost float64)
}

// NewAIAgent connects a realtime provider session plus a visible LiveKit
// participant and starts the agent's loops.
//
// The order matters: the LiveKit participant is registered first, then the
// voice path is opened (which, with an avatar, dials the avatar provider right
// away), and only then is the provider session opened. That way a failing
// avatar API aborts the start before the expensive realtime session exists, and
// the avatar worker joins the same room right behind the agent.
func NewAIAgent(args *AIAgentArgs) (*AIAgent, error) {
	if args == nil || args.Payload == nil || args.Payload.RoomId == "" {
		return nil, errors.New("ai agent: room id is required")
	}
	if args.Features == nil {
		return nil, errors.New("ai agent: features are required")
	}
	if args.ServiceConfig == nil || args.ProviderAccount == nil {
		return nil, errors.New("ai agent: provider is not configured")
	}

	log := args.Logger.WithFields(logrus.Fields{
		"service": "ai-agent",
		"roomId":  args.Payload.RoomId,
	})
	boundary := args.Boundary.withDefaults()

	// External MCP servers are discovered once, at room creation. This only
	// warms what is still missing, and the registry afterwards reads Redis only,
	// so a slow remote server never delays the realtime session.
	mcp.EnsureRoomExternalMCP(args.Ctx, args.Deps.RedisService, args.Payload.RoomId, args.Features.GetMcpServers(), args.Features.GetMaxSessionSeconds(), log, args.Deps.ExternalURLHook)

	// The provider receives the compiled envelope, never the host prompt verbatim.
	registry := mcp.BuildRoomToolRegistry(args.Features, args.Deps)
	opts := &insights.RealtimeSessionOptions{
		SystemInstruction:  BuildLockedSystemInstruction(boundary),
		Voice:              args.Features.GetVoice(),
		SpokenLang:         boundary.SpokenLang,
		TargetLang:         boundary.TargetLang,
		InstructionLang:    boundary.InstructionLang,
		SpeakingRate:       boundary.SpeakingRate,
		Tools:              registry.Specs(),
		MaxSessionDuration: time.Duration(args.Features.GetMaxSessionSeconds()) * time.Second,
	}

	provider, err := NewProvider(&ProviderArgs{
		Ctx:             args.Ctx,
		ProviderType:    args.ServiceConfig.Provider,
		ProviderAccount: args.ProviderAccount,
		ServiceConfig:   args.ServiceConfig,
		Logger:          log,
		RDS:             args.RedisService.GetRedisClient(),
	})
	if err != nil {
		return nil, fmt.Errorf("ai agent: failed to create provider: %w", err)
	}

	model := args.ServiceConfig.GetOptionsString("realtime_model", args.ServiceConfig.GetOptionsString("model", ""))
	newSession := func(ctx context.Context) (insights.RealtimeSession, error) {
		return provider.CreateRealtimeSession(ctx, model, opts)
	}

	// The running summary is a background text call, so the voice session never
	// waits on it.
	var summarize func(ctx context.Context, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error)
	if model := firstNonEmpty(args.SummarizeModel, args.ServiceConfig.GetOptionsString("summarize_model", "")); model != "" {
		summarize = func(sumCtx context.Context, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
			return provider.AIChatTextSummarize(sumCtx, model, history)
		}
	}

	// No Session: the provider session is opened after the room participant and
	// the voice path exist, so the participant is visible immediately and a
	// broken avatar provider fails before the session is billed.
	agent, err := newAIAgent(&aiAgentCoreArgs{
		Ctx:               args.Ctx,
		Payload:           args.Payload,
		Features:          args.Features,
		Boundary:          boundary,
		Session:           nil,
		NewSession:        newSession,
		Transport:         args.Transport,
		RedisService:      args.RedisService,
		Logger:            log,
		Registry:          registry,
		ClientRPC:         args.Deps.Client,
		OnStopped:         args.OnStopped,
		AgentUserId:       args.AgentUserId,
		Learner:           args.Learner,
		MaxAudioMinute:    args.Features.GetMaxAudioMinutes(),
		CustomProvider:    args.CustomProvider,
		Summarize:         summarize,
		SummarizeWindow:   args.ServiceConfig.GetIntOption("context_window", defaultAISummarizeWindow),
		PreviousTelemetry: args.PreviousTelemetry,
		Avatar:            args.Avatar,
		EstimateCost:      args.EstimateCost,
	})
	if err != nil {
		return nil, err
	}

	// A visible participant: participants see and hear the agent.
	payload := args.Payload
	payload.CaptureAllParticipantsTracks = true
	payload.HiddenAgent = false
	displayName := boundary.AgentDisplayName
	payload.AgentName = &displayName

	// Step 1: the agent joins LiveKit first, so its identity is registered before anything else is dialed.
	roomAgent, err := newRoomAgent(&RoomAgentArgs{
		Ctx:             agent.ctx,
		AppConf:         args.AppConf,
		NatsConn:        args.NatsConn,
		JS:              args.JS,
		ServiceConfig:   args.ServiceConfig,
		ProviderAccount: args.ProviderAccount,
		NatsService:     args.NatsService,
		RedisService:    args.RedisService,
		Logger:          log,
		Payload:         payload,
		AgentUserId:     agent.agentUserId,
	}, &aiAgentAudioTask{sink: agent.pushAudio})
	if err != nil {
		agent.close()
		return nil, fmt.Errorf("ai agent: failed to join the room: %w", err)
	}

	// Step 2: open the voice path. With an avatar this dials the avatar provider immediately, so the avatar worker boots
	// and joins the same room while the realtime session is still being opened.
	publisher, err := startAgentAudioPublisher(args, agent, roomAgent.Room, displayName)
	if err != nil {
		roomAgent.Shutdown()
		agent.close()
		return nil, err
	}

	// Step 3: only now open the realtime provider session.
	session, err := newSession(agent.ctx)
	if err != nil {
		publisher.Close()
		roomAgent.Shutdown()
		agent.close()
		return nil, fmt.Errorf("ai agent: failed to open realtime session: %w", err)
	}
	agent.session = session

	// Step 4: wire the runtime and start the loops.
	agent.roomAgent = roomAgent
	agent.publisher = publisher
	agent.Start()

	// The room connection dropping is not a graceful stop; make it one.
	go func() {
		<-roomAgent.Ctx.Done()
		agent.Stop("disconnected")
	}()

	return agent, nil
}

// startAgentAudioPublisher opens the way the agent voice reaches the room.
// Without an avatar the agent publishes a normal microphone track. With one, the avatar provider owns the video:
// its bot joins the room as the agent's native twin, and the agent audio is streamed to that participant instead of being
// published, so the lip-sync follows the agent's own speech.
func startAgentAudioPublisher(args *AIAgentArgs, agent *AIAgent, room *lksdk.Room, displayName string) (audioPublisher, error) {
	if args.Avatar == nil || args.Avatar.Provider == "" {
		publisher, err := insightsmedia.NewAudioPublisher(room, aiAgentTrackName, insightsmedia.DefaultOutputSampleRate24k, 1, args.Payload.RoomE2EEKey)
		if err != nil {
			return nil, fmt.Errorf("ai agent: failed to publish the voice track: %w", err)
		}
		return publisher, nil
	}

	// The twin joins with its own token, minted after the agent itself is
	// connected: the twin is what the audio stream is addressed to.
	twinIdentity := config.GetNativeTwinIdentity(agent.agentUserId)
	// The twin has to outlive the session, else the avatar drops mid-lesson
	// while the agent keeps talking.
	validity := time.Duration(args.Features.GetMaxSessionSeconds())*time.Second + 10*time.Minute
	if validity < time.Hour {
		validity = time.Hour
	}
	twinToken, err := mintAvatarLiveKitAccessToken(args.AppConf.LivekitInfo.ApiKey, args.AppConf.LivekitInfo.Secret, validity, args.Payload.RoomId, twinIdentity, displayName, agent.agentUserId)
	if err != nil {
		return nil, fmt.Errorf("ai agent: failed to mint the avatar participant token: %w", err)
	}

	startFn := args.Avatar.StartFunc
	if startFn == nil {
		startFn = avatarservice.StartSession
	}
	avatarSession, err := startFn(agent.ctx, avatarservice.StartConfig{
		Provider:     args.Avatar.Provider,
		APIKey:       args.Avatar.APIKey,
		AvatarID:     args.Avatar.AvatarID,
		Endpoint:     args.Avatar.Endpoint,
		AuthHeader:   args.Avatar.AuthHeader,
		LiveKitURL:   args.AppConf.LivekitInfo.Host,
		LiveKitToken: twinToken,
		LiveKitRoom:  args.Payload.RoomId,
	})
	if err != nil {
		return nil, fmt.Errorf("ai agent: failed to start the avatar: %w", err)
	}

	// A session with its own websocket streams the voice itself; every other
	// provider gets it over the LiveKit datastream addressed to the twin.
	var publisher audioPublisher
	if publisherProvider, ok := avatarSession.(sessionAudioPublisherProvider); ok {
		publisher = publisherProvider.AudioPublisher()
	} else {
		datastreamPublisher, err := insightsmedia.NewDataStreamAudioPublisher(room, twinIdentity, avatarSession.SampleRate())
		if err != nil {
			_ = avatarSession.Close(context.Background())
			return nil, fmt.Errorf("ai agent: failed to stream the voice to the avatar: %w", err)
		}
		publisher = datastreamPublisher
	}

	agent.avatarSession = avatarSession
	agent.avatarStartedAt = time.Now()

	return publisher, nil
}

// mintAvatarLiveKitAccessToken mints the join token for the agent's native twin,
// built directly with lkauth so it always sets original_user_id and
// lk.publish_on_behalf, and joins as kind AGENT (very important!).
func mintAvatarLiveKitAccessToken(apiKey, secret string, tokenValidity time.Duration, roomId, twinIdentity, displayName, agentUserId string) (string, error) {
	at := lkauth.NewAccessToken(apiKey, secret)
	// The twin only streams the voice and the data of the agent, so it joins with
	// media permissions and no room administration.
	grant := &lkauth.VideoGrant{
		RoomJoin:       true,
		Room:           roomId,
		CanPublish:     new(true),
		CanPublishData: new(true),
		CanSubscribe:   new(true),
	}
	at.SetVideoGrant(grant).
		SetIdentity(twinIdentity).
		SetName(displayName).
		SetValidFor(tokenValidity).
		SetKind(livekit.ParticipantInfo_AGENT).
		SetAttributes(map[string]string{
			"original_user_id":     agentUserId,
			"lk.publish_on_behalf": agentUserId,
		})
	return at.ToJWT()
}

// newAIAgent builds the provider- and LiveKit-independent runtime.
// It may be built before the provider session is open: with args.NewSession set the caller opens the session later and
// assigns it before calling Start, which is what puts the LiveKit participant and the avatar ahead of the provider.
func newAIAgent(args *aiAgentCoreArgs) (*AIAgent, error) {
	if args == nil || args.Payload == nil || args.Payload.RoomId == "" {
		return nil, errors.New("ai agent: room id is required")
	}
	if args.Session == nil && args.NewSession == nil {
		return nil, errors.New("ai agent: a realtime session is required")
	}

	ctx, cancel := context.WithCancel(args.Ctx)
	sessionId := args.SessionId
	if sessionId == "" {
		sessionId = uuid.NewString()
	}
	agentUserId := args.AgentUserId
	if agentUserId == "" {
		agentUserId = fmt.Sprintf("%s%s-%s", config.AgentUserUserIdPrefix, insights.ServiceTypeAIAgent, uuid.NewString())
	}
	boundary := args.Boundary.withDefaults()
	log := args.Logger.WithFields(logrus.Fields{
		"service":     "ai-agent",
		"roomId":      args.Payload.RoomId,
		"sessionId":   sessionId,
		"agentUserId": agentUserId,
	})

	registry := args.Registry
	if registry == nil {
		registry = mcp.NewRegistry()
	}
	guard := NewBoundaryGuard(boundary)

	maxAudioMinutes := args.MaxAudioMinute
	if maxAudioMinutes == 0 {
		maxAudioMinutes = aiAgentDefaultMaxAudioMinutes
	}

	features := args.Features
	if features == nil {
		features = &plugnmeet.InsightsAIAgentFeatures{}
	}

	agent := &AIAgent{
		ctx:             ctx,
		cancel:          cancel,
		logger:          log,
		roomId:          args.Payload.RoomId,
		sessionId:       sessionId,
		agentUserId:     agentUserId,
		agentName:       boundary.AgentDisplayName,
		startedAt:       time.Now(),
		sessionOpenedAt: time.Now(),
		boundary:        boundary,
		features:        features,
		guard:           guard,
		transport:       args.Transport,
		onStopped:       args.OnStopped,
		learner:         args.Learner,
		maxAudio:        time.Duration(maxAudioMinutes) * time.Minute,
		redisService:    args.RedisService,
		estimateCost:    args.EstimateCost,
		customProvider:  args.CustomProvider,
		summarize:       args.Summarize,
		summarizeWindow: args.SummarizeWindow,
		inAudio:         make(chan mediasdk.PCM16Sample, aiAgentInAudioBuffer),
		session:         args.Session,
		newSession:      args.NewSession,
		done:            make(chan struct{}),
		state: aiAgentRuntimeState{
			state:     plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_CONNECTING,
			toolsUsed: make(map[string]int),
		},
	}
	if agent.summarizeWindow <= 0 {
		agent.summarizeWindow = defaultAISummarizeWindow
	}
	// An avatar is billed and budgeted from the moment the session starts, so a
	// caller that builds the core directly tracks its duration too.
	if args.Avatar != nil && args.Avatar.Provider != "" {
		agent.avatarProvider = string(args.Avatar.Provider)
		if args.Avatar.IsCustom {
			agent.customAvatarProvider = string(args.Avatar.Provider)
		}
		if args.Avatar.MaxMinutes > 0 {
			agent.maxAvatar = time.Duration(args.Avatar.MaxMinutes) * time.Minute
		}
		agent.avatarStartedAt = agent.startedAt
	}
	// A restart in the same room continues the earlier session, so the opening
	// telemetry flush carries its usage and conversation instead of zeroes.
	if prev := args.PreviousTelemetry; prev != nil {
		agent.resuming = true
		agent.seedStateFromTelemetry(prev)
	}

	// The sentinel tool is declared to the provider and handled inline by the loop.
	registry.Register(reportOffTopicTool(guard))
	agent.executor = mcp.NewExecutor(mcp.ExecutorOptions{
		Registry:    registry,
		Client:      args.ClientRPC,
		ScopeLocked: guard.IsToolScopeLocked,
	})

	return agent, nil
}

// optionalNonEmptyString converts an empty string into a nil pointer, so an
// optional proto field stays absent instead of serialized as empty.
func optionalNonEmptyString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// RoomID returns the room this agent serves.
func (a *AIAgent) RoomID() string { return a.roomId }

// SessionID returns the provider session id of this activation.
func (a *AIAgent) SessionID() string { return a.sessionId }

// AgentUserID returns the LiveKit identity of the agent participant.
func (a *AIAgent) AgentUserID() string { return a.agentUserId }

// AgentName returns the display name participants see.
func (a *AIAgent) AgentName() string { return a.agentName }

// Done is closed once the agent has fully stopped.
func (a *AIAgent) Done() <-chan struct{} { return a.done }

// SetLearner records the learner of the session and flushes the telemetry, so
// the paired participant reaches Redis as soon as the pairing happens. Once the
// participant is known only missing details are accepted: a supervisor joining
// to observe must not become the learner, and a partial lookup must not erase
// the identity captured when the session started.
func (a *AIAgent) SetLearner(identity LearnerIdentity) {
	if a == nil {
		return
	}
	a.learnerMu.Lock()
	if a.learner.UserID != "" && identity.UserID != "" && identity.UserID != a.learner.UserID {
		a.learnerMu.Unlock()
		return
	}
	previous := a.learner
	updated := identity.WithFallback(a.learner)
	changed := updated != a.learner
	a.learner = updated
	a.learnerMu.Unlock()

	if !changed {
		return
	}
	// A session started before the learner was known captured the whole room;
	// now that the pairing exists, that participant's audio is allowed.
	if previous.UserID == "" && updated.UserID != "" && a.roomAgent != nil {
		a.roomAgent.AllowTargetUser(updated.UserID)
	}
	// persistTelemetry reads the learner, so it cannot run under learnerMu.
	a.persistTelemetry("")
}

// Learner returns the learner identity known to the session.
func (a *AIAgent) Learner() LearnerIdentity {
	a.learnerMu.Lock()
	defer a.learnerMu.Unlock()
	return a.learner
}

// Start launches the audio input loop, the provider event loop and the session
// budget timer. It is safe to call once.
func (a *AIAgent) Start() {
	a.startOnce.Do(func() {
		registerActiveAIAgent(a)
		// An empty end reason marks this as the opening snapshot, so a crash still
		// leaves the session's identity and topic behind.
		a.persistTelemetry("")

		a.loopMu.Lock()
		loop := a.trackLoop(a.session)
		a.loopMu.Unlock()

		a.wg.Add(2)
		go func() {
			defer a.wg.Done()
			a.writeAudioLoop()
		}()
		go func() {
			defer a.wg.Done()
			a.readEventLoop(loop)
		}()

		// A session restarted in the same room opens with the conversation so far,
		// so the agent continues it instead of introducing itself again.
		if a.resuming {
			if handoff := a.buildReconnectHandoff(); handoff != "" {
				if err := a.sendContextText(handoff); err != nil {
					a.logger.WithError(err).Debug("ai agent: failed to inject the reconnect handoff")
				}
			}
		}

		if budget := a.sessionBudget(); budget > 0 {
			a.sessionTimer = time.AfterFunc(budget, func() {
				a.logger.Info("ai agent: session budget reached")
				a.Stop("budget")
			})
		}
		// The avatar is billed separately and may have less left than the
		// session itself, so it gets its own budget.
		if a.maxAvatar > 0 {
			remaining := a.maxAvatar - time.Duration(a.priorAvatarMillis)*time.Millisecond
			if remaining <= 0 {
				a.logger.Info("ai agent: avatar budget reached")
				a.Stop("budget")
				return
			}
			a.avatarTimer = time.AfterFunc(remaining, func() {
				a.logger.Info("ai agent: avatar budget reached")
				a.Stop("budget")
			})
		}
		a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
	})
}

// Stop ends the session exactly once and releases every resource.
func (a *AIAgent) Stop(reason string) {
	a.stopOnce.Do(func() {
		a.logger.WithField("reason", reason).Info("ai agent: stopping")
		a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_STOPPED, reason)

		if a.sessionTimer != nil {
			a.sessionTimer.Stop()
		}
		if a.avatarTimer != nil {
			a.avatarTimer.Stop()
		}
		// No summary may join after this point, and the cancelled context ends the
		// one still running.
		a.summarizeMu.Lock()
		a.summarizeStop = true
		a.summarizeMu.Unlock()

		a.cancel()

		if session := a.currentSession(); session != nil {
			if err := session.Close(); err != nil {
				a.logger.WithError(err).Debug("ai agent: failed to close the realtime session")
			}
		}
		if a.roomAgent != nil {
			a.roomAgent.Shutdown()
		}
		if a.publisher != nil {
			a.publisher.Close()
		}
		// The avatar bot leaves with its own session; the shutdown of the room
		// connection above only disconnects the agent itself.
		if a.avatarSession != nil {
			_ = a.avatarSession.Close(context.Background())
		}

		// The tool serialization lock is keyed by the provider session id of the
		// calls, so it goes with the session.
		if a.executor != nil {
			a.executor.ReleaseSession(a.sessionId)
		}

		unregisterActiveAIAgent(a)
		a.wg.Wait()
		// A summary still in flight updates the state, so wait for it before the
		// closing flush reads it.
		a.summarizeWg.Wait()
		// Flush before logUsage so the stored snapshot matches what is logged.
		a.persistTelemetry(reason)
		a.logUsage()
		close(a.done)

		if a.onStopped != nil {
			a.onStopped(reason)
		}
	})
}

// close releases resources when construction failed after the core was built.
func (a *AIAgent) close() {
	a.cancel()
	if session := a.currentSession(); session != nil {
		_ = session.Close()
	}
	if a.executor != nil {
		a.executor.ReleaseSession(a.sessionId)
	}
}

// Pause keeps the provider session open but stops audio in and out.
func (a *AIAgent) Pause() error {
	if !a.paused.CompareAndSwap(false, true) {
		return nil
	}
	if a.publisher != nil {
		a.publisher.Flush()
	}
	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_PAUSED, "")
	return nil
}

// Resume re-enables audio and re-injects a fresh anchor before the next turn.
func (a *AIAgent) Resume() error {
	if !a.paused.CompareAndSwap(true, false) {
		return nil
	}
	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
	return a.sendContextText(BuildTopicAnchorMessageWithProgress(a.boundary, a.guard.TurnCount(), a.guard.ActivityProgress()))
}

// Status reports the current runtime state of the agent.
func (a *AIAgent) Status() *plugnmeet.AIAgentStatusRes {
	a.stateMu.Lock()
	snapshot := a.state
	a.stateMu.Unlock()

	return &plugnmeet.AIAgentStatusRes{
		Status: true,
		Msg:    config.MsgSuccess,
		State: &plugnmeet.AIAgentStateMsg{
			SessionId:      a.sessionId,
			AgentUserId:    a.agentUserId,
			AgentName:      a.agentName,
			State:          snapshot.state,
			IsSpeaking:     snapshot.speaking,
			ToolInProgress: snapshot.tool,
			StartedAt:      a.startedAt.UnixMilli(),
			Reason:         snapshot.reason,
			AvatarProvider: optionalNonEmptyString(a.avatarProvider),
		},
	}
}

// AvatarProvider returns the avatar provider of the session, empty without one.
func (a *AIAgent) AvatarProvider() string { return a.avatarProvider }

func (a *AIAgent) markSpeaking() {
	a.stateMu.Lock()
	changed := !a.state.speaking || a.state.state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.speaking = true
	a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.tool = ""
	a.pendingThinking = false
	// Every audio chunk postpones the fallback, including the queued playout, so
	// the fallback never fires mid-sentence.
	a.rearmSpeakingTimerLocked(a.speakingIdleDelay())
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.broadcast(snapshot)
	}
}

// speakingIdleDelay is the silence tolerated while SPEAKING, extended by the
// audio still queued in the publisher.
func (a *AIAgent) speakingIdleDelay() time.Duration {
	if rem := a.remainingPlayout(); rem > 0 {
		return rem + aiAgentSpeakingIdle
	}
	return aiAgentSpeakingIdle
}

// endIdleSpeaking restores LISTENING once the agent audio stream has been silent
// for longer than any gap between chunks, so a trailing delta or a missing
// turn-complete cannot leave the status stuck on SPEAKING. A turn that was
// deferred because its filler was still playing moves on to THINKING instead.
func (a *AIAgent) endIdleSpeaking() {
	if a.paused.Load() {
		return
	}

	a.stateMu.Lock()
	if a.state.state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING && !a.pendingThinking && !a.state.speaking {
		a.stateMu.Unlock()
		return
	}
	if rem := a.remainingPlayout(); rem > 0 {
		a.rearmSpeakingTimerLocked(rem + aiAgentPlayoutGrace)
		a.stateMu.Unlock()
		return
	}
	if a.pendingThinking {
		// The deferred turn is still composing, so the status moves on to
		// THINKING instead of falling back to LISTENING. SPEAKING has to go
		// first, otherwise markThinking would defer itself again.
		a.pendingThinking = false
		a.state.speaking = false
		a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
		a.state.tool = ""
		a.state.reason = ""
		a.stateMu.Unlock()

		a.markThinking()
		return
	}
	// A tool or thinking state kept speaking while its audio played out, so the
	// status now falls back to the label it already holds.
	if a.state.speaking && a.state.state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING {
		a.state.speaking = false
		snapshot := a.state
		a.stateMu.Unlock()
		a.broadcast(snapshot)
		return
	}
	a.stateMu.Unlock()

	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
}

// finishSpeakingOrListen ends a turn after its turn-complete. SPEAKING is held
// while the queued audio still plays out, so the status does not reach LISTENING
// before the listener hears the last frame.
func (a *AIAgent) finishSpeakingOrListen() {
	if a.paused.Load() {
		return
	}

	a.stateMu.Lock()
	// The turn is over, so nothing from it is left to think about.
	a.pendingThinking = false
	switch a.state.state {
	case plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING:
		if rem := a.remainingPlayout(); rem > 0 {
			a.rearmSpeakingTimerLocked(rem + aiAgentPlayoutGrace)
			a.stateMu.Unlock()
			return
		}
	case plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING:
	default:
		a.stateMu.Unlock()
		return
	}
	a.stateMu.Unlock()

	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
}

// markThinking reports that the model is composing a turn.
func (a *AIAgent) markThinking() {
	a.enterThinking()
}

// finishToolCall clears a finished tool from the status. Whether the answer
// follows is the provider's call: a tool result that resumes generation is
// reported as THINKING by the provider, so the cleared label only gives way to
// the speech still playing out, and then to LISTENING.
func (a *AIAgent) finishToolCall(tool string) {
	if a.paused.Load() {
		return
	}

	a.stateMu.Lock()
	if a.state.tool != tool {
		// The provider already moved the status on, or another tool owns it.
		a.stateMu.Unlock()
		return
	}
	playout := a.remainingPlayout()
	deferred := a.pendingThinking
	a.state.tool = ""
	switch {
	case deferred:
		// The follow-up turn waits for the queued speech to drain.
	case playout > 0:
		// The turn that called the tool is still being heard.
		a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
		a.state.speaking = true
		a.rearmSpeakingTimerLocked(playout + aiAgentPlayoutGrace)
	default:
		a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
		a.state.speaking = false
		a.state.reason = ""
	}
	snapshot := a.state
	a.stateMu.Unlock()

	a.logger.WithFields(logrus.Fields{
		"state":      snapshot.state.String(),
		"tool":       "",
		"isSpeaking": snapshot.speaking,
	}).Debug("ai agent: state changed")
	a.broadcast(snapshot)
}

func (a *AIAgent) enterThinking() {
	if a.paused.Load() {
		return
	}

	a.stateMu.Lock()
	if a.state.state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING || a.remainingPlayout() > 0 {
		// Queued audio is still heard as speech, so the transition waits until
		// it drained (see endIdleSpeaking).
		a.pendingThinking = true
		a.stateMu.Unlock()
		return
	}
	// A running tool, or the drawing it started, owns the status: only
	// finishToolCall gives way to the answer.
	if a.state.tool != "" || a.state.state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING {
		a.stateMu.Unlock()
		return
	}
	changed := a.state.state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING || a.state.tool != ""
	a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING
	a.state.speaking = false
	a.state.tool = ""
	a.state.reason = ""
	a.stopSpeakTimerLocked()
	a.speakTimer = time.AfterFunc(aiAgentThinkingIdle, a.endIdleThinking)
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.logger.WithField("state", snapshot.state.String()).Debug("ai agent: state changed")
	}
	a.broadcast(snapshot)
}

// endIdleThinking leaves THINKING when a turn neither speaks nor completes.
func (a *AIAgent) endIdleThinking() {
	if a.paused.Load() {
		return
	}
	a.stateMu.Lock()
	thinking := a.state.state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING
	a.stateMu.Unlock()
	if !thinking {
		return
	}
	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
}

// remainingPlayout reports how much agent audio the listener has not heard yet.
func (a *AIAgent) remainingPlayout() time.Duration {
	if a.publisher == nil {
		return 0
	}
	return a.publisher.RemainingDuration()
}

func (a *AIAgent) rearmSpeakingTimerLocked(delay time.Duration) {
	a.stopSpeakTimerLocked()
	a.speakTimer = time.AfterFunc(delay, a.endIdleSpeaking)
}

func (a *AIAgent) stopSpeakTimerLocked() {
	if a.speakTimer != nil {
		a.speakTimer.Stop()
		a.speakTimer = nil
	}
}

func (a *AIAgent) setState(state plugnmeet.AIAgentRuntimeState, reason string) {
	a.stateMu.Lock()
	if state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING {
		a.stopSpeakTimerLocked()
	}
	a.pendingThinking = false
	changed := a.state.state != state || a.state.tool != "" || a.state.reason != reason
	a.state.state = state
	a.state.speaking = state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.tool = ""
	a.state.reason = reason
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.logger.WithFields(logrus.Fields{"state": state.String(), "tool": "", "reason": reason}).Debug("ai agent: state changed")
	}
	a.broadcast(snapshot)
}

func (a *AIAgent) setToolState(state plugnmeet.AIAgentRuntimeState, tool string) {
	a.stateMu.Lock()
	a.pendingThinking = false
	changed := a.state.state != state || a.state.tool != tool || a.state.reason != ""
	a.state.state = state
	// A tool call can arrive while the sentence announcing it is still playing
	// out, so the tool label must not cut the speech short. endIdleSpeaking
	// clears the flag and publishes the tool state once the playout drained.
	a.state.speaking = false
	if rem := a.remainingPlayout(); rem > 0 {
		a.state.speaking = true
		a.rearmSpeakingTimerLocked(rem + aiAgentPlayoutGrace)
	} else {
		// A running tool drives the next transition, so no idle fallback may
		// replace its label.
		a.stopSpeakTimerLocked()
	}
	a.state.tool = tool
	a.state.reason = ""
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.logger.WithFields(logrus.Fields{"state": state.String(), "tool": tool, "reason": ""}).Debug("ai agent: state changed")
	}
	a.broadcast(snapshot)
}

// tavusEmotionForState maps a runtime state to the emotion of the avatar: it smiles while speaking and stays calm otherwise.
func tavusEmotionForState(state plugnmeet.AIAgentRuntimeState) string {
	if state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING {
		return avatarservice.TavusEmotionHappiness
	}

	return avatarservice.TavusEmotionNeutral
}

// syncAvatarState forwards the runtime state to the avatar: a websocket avatar
// learns when to listen, a LiveKit avatar gets the matching emotion.
func (a *AIAgent) syncAvatarState(snapshot aiAgentRuntimeState) {
	if setter, ok := a.avatarSession.(listeningSetter); ok {
		setter.SetListening(snapshot.state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING)
	}

	a.syncAvatarEmotion(snapshot)
}

// syncAvatarEmotion forwards the state as an emotion to an avatar that listens
// on the LiveKit app messages topic.
// The emotion is recorded only once the avatar accepted it: a bot that is still joining drops the message,
// so the next state change sends it again.
func (a *AIAgent) syncAvatarEmotion(snapshot aiAgentRuntimeState) {
	if a.avatarProvider != string(config.ProviderTavus) && a.avatarProvider != string(config.ProviderLiveKitDataStream) {
		return
	}
	publisher, ok := a.publisher.(appMessagePublisher)
	if !ok {
		return
	}

	emotion := tavusEmotionForState(snapshot.state)
	a.emotionMu.Lock()
	sent := a.avatarEmotion == emotion
	a.emotionMu.Unlock()
	if sent {
		return
	}

	payload, err := avatarservice.TavusEmotionPayload(emotion)
	if err != nil {
		return
	}
	if err := publisher.SendAppMessage(avatarservice.TavusAppMessagesTopic, payload); err != nil {
		a.logger.WithError(err).Debug("ai agent: the avatar emotion could not be sent")
		return
	}

	a.emotionMu.Lock()
	a.avatarEmotion = emotion
	a.emotionMu.Unlock()
}

func (a *AIAgent) broadcast(snapshot aiAgentRuntimeState) {
	a.syncAvatarState(snapshot)

	if a.transport == nil {
		return
	}
	err := a.transport.BroadcastAIAgentState(a.roomId, &plugnmeet.AIAgentStateMsg{
		SessionId:      a.sessionId,
		AgentUserId:    a.agentUserId,
		AgentName:      a.agentName,
		State:          snapshot.state,
		IsSpeaking:     snapshot.speaking,
		ToolInProgress: snapshot.tool,
		StartedAt:      a.startedAt.UnixMilli(),
		Reason:         snapshot.reason,
		AvatarProvider: optionalNonEmptyString(a.avatarProvider),
	})
	if err != nil {
		a.logger.WithError(err).Debug("ai agent: failed to broadcast the agent state")
	}
}
