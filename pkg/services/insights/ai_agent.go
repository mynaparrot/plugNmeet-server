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
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsmedia "github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const (
	// aiAgentTrackName is the published microphone track participants hear.
	aiAgentTrackName = "ai-agent-voice"
	// aiAgentInAudioBuffer bounds the participant audio waiting for the provider.
	// Audio older than roughly two seconds is worthless, so overflow drops the
	// oldest chunk instead of blocking the RTP reader.
	aiAgentInAudioBuffer = 32
	// aiAgentWhiteboardToolPrefix marks the client-delegated whiteboard tools.
	aiAgentWhiteboardToolPrefix = "whiteboard_"
	// aiAgentDefaultMaxAudioMinutes mirrors the documented provider budget.
	aiAgentDefaultMaxAudioMinutes = 120
	// maxSessionTranscriptTurns bounds the transcript kept for the summary; the
	// tail is what a handoff needs, and the whole call would bloat Redis.
	maxSessionTranscriptTurns = 40
	// maxTranscriptTurnChars bounds one turn so a rambling answer cannot dominate
	// the payload.
	maxTranscriptTurnChars = 240
	// aiAgentTelemetryTimeout bounds the flush that outlives a cancelled session.
	aiAgentTelemetryTimeout = 3 * time.Second
	// defaultAISummarizeWindow is how many unsummarized transcript turns trigger
	// the background summary: eight turns are four question-answer exchanges.
	defaultAISummarizeWindow = 8
	// aiAgentSummarizeTimeout bounds one background summary, so a slow model can
	// never hold the session.
	aiAgentSummarizeTimeout = 5 * time.Second

	transcriptRoleParticipant = "participant"
	transcriptRoleAgent       = "agent"
)

// aiAgentSpeakingIdle is how long the agent may stay silent before the status
// falls back from SPEAKING to LISTENING. Providers stream audio in 20-200ms
// chunks, so a longer gap means the turn ended — even when no turn-complete
// event follows. A variable so tests can shorten it.
var aiAgentSpeakingIdle = 1500 * time.Millisecond

// AIAgentTransport is the room-facing messaging an AI agent needs.
// *natsservice.NatsService implements it; tests inject a recorder.
type AIAgentTransport interface {
	BroadcastAIAgentState(roomId string, msg *plugnmeet.AIAgentStateMsg) error
	SendAIAgentToolCallToUser(roomId, userId string, msg *plugnmeet.AIAgentToolCallMsg) error
}

// audioPublisher is the subset of media.AudioPublisher the agent needs.
type audioPublisher interface {
	Write(data []byte) (int, error)
	Flush()
	Close()
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
	delegate  func() (string, error)
	onStopped func(reason string)
	maxAudio  time.Duration
	// redisService persists the session telemetry; nil in tests without a store.
	redisService *redisservice.RedisService

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

	learnerMu sync.Mutex
	learner   LearnerIdentity

	pendingMu    sync.Mutex
	pendingCalls map[string]chan *plugnmeet.AIAgentToolResultMsg

	paused       atomic.Bool
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
	// Delegate resolves the participant that executes client-delegated tools.
	Delegate func() (string, error)
	// OnStopped runs once after the agent has fully stopped.
	OnStopped func(reason string)
	// SummarizeModel refines the running conversation summary; an empty model
	// disables the background summarization.
	SummarizeModel string
	// PreviousTelemetry is the room's earlier session. A restart in the same
	// room continues its conversation and its token usage instead of wiping them.
	PreviousTelemetry *redisservice.AIAgentSessionTelemetry
}

// aiAgentCoreArgs is everything the agent needs that does not depend on a
// LiveKit connection, so the runtime loop is testable without a media server.
type aiAgentCoreArgs struct {
	Ctx            context.Context
	Payload        *insights.InsightsTaskPayload
	Features       *plugnmeet.InsightsAIAgentFeatures
	Boundary       BoundaryConfig
	Session        insights.RealtimeSession
	NewSession     func(ctx context.Context) (insights.RealtimeSession, error)
	Transport      AIAgentTransport
	RedisService   *redisservice.RedisService
	Logger         *logrus.Entry
	Registry       *mcp.Registry
	Delegate       func() (string, error)
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
}

// NewAIAgent connects a realtime provider session plus a visible LiveKit
// participant and starts the agent's loops.
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
	mcp.EnsureRoomExternalMCP(args.Ctx, args.Deps.RedisService, args.Payload.RoomId, args.Features.GetMcpServers(), args.Features.GetMaxSessionSeconds(), log)

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
	session, err := newSession(args.Ctx)
	if err != nil {
		return nil, fmt.Errorf("ai agent: failed to open realtime session: %w", err)
	}

	// The running summary is a background text call, so the voice session never
	// waits on it.
	var summarize func(ctx context.Context, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error)
	if model := firstNonEmpty(args.SummarizeModel, args.ServiceConfig.GetOptionsString("summarize_model", "")); model != "" {
		summarize = func(sumCtx context.Context, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
			return provider.AIChatTextSummarize(sumCtx, model, history)
		}
	}

	agent, err := newAIAgent(&aiAgentCoreArgs{
		Ctx:               args.Ctx,
		Payload:           args.Payload,
		Features:          args.Features,
		Boundary:          boundary,
		Session:           session,
		NewSession:        newSession,
		Transport:         args.Transport,
		RedisService:      args.RedisService,
		Logger:            log,
		Registry:          registry,
		Delegate:          args.Delegate,
		OnStopped:         args.OnStopped,
		AgentUserId:       args.AgentUserId,
		Learner:           args.Learner,
		MaxAudioMinute:    args.Features.GetMaxAudioMinutes(),
		CustomProvider:    args.CustomProvider,
		Summarize:         summarize,
		SummarizeWindow:   args.ServiceConfig.GetIntOption("context_window", defaultAISummarizeWindow),
		PreviousTelemetry: args.PreviousTelemetry,
	})
	if err != nil {
		_ = session.Close()
		return nil, err
	}

	// A visible participant: participants see and hear the agent.
	payload := args.Payload
	payload.CaptureAllParticipantsTracks = true
	payload.HiddenAgent = false
	displayName := boundary.AgentDisplayName
	payload.AgentName = &displayName

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

	publisher, err := insightsmedia.NewAudioPublisher(roomAgent.Room, aiAgentTrackName, insightsmedia.DefaultOutputSampleRate24k, 1, payload.RoomE2EEKey)
	if err != nil {
		roomAgent.Shutdown()
		agent.close()
		return nil, fmt.Errorf("ai agent: failed to publish the voice track: %w", err)
	}

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

// newAIAgent builds the provider- and LiveKit-independent runtime.
func newAIAgent(args *aiAgentCoreArgs) (*AIAgent, error) {
	if args == nil || args.Payload == nil || args.Payload.RoomId == "" {
		return nil, errors.New("ai agent: room id is required")
	}
	if args.Session == nil {
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
		boundary:        boundary,
		features:        features,
		guard:           guard,
		transport:       args.Transport,
		delegate:        args.Delegate,
		onStopped:       args.OnStopped,
		learner:         args.Learner,
		maxAudio:        time.Duration(maxAudioMinutes) * time.Minute,
		redisService:    args.RedisService,
		customProvider:  args.CustomProvider,
		summarize:       args.Summarize,
		summarizeWindow: args.SummarizeWindow,
		inAudio:         make(chan mediasdk.PCM16Sample, aiAgentInAudioBuffer),
		session:         args.Session,
		newSession:      args.NewSession,
		pendingCalls:    make(map[string]chan *plugnmeet.AIAgentToolResultMsg),
		done:            make(chan struct{}),
		state: aiAgentRuntimeState{
			state:     plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_CONNECTING,
			toolsUsed: make(map[string]int),
		},
	}
	if agent.summarizeWindow <= 0 {
		agent.summarizeWindow = defaultAISummarizeWindow
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
		Client:      agent,
		ScopeLocked: guard.IsToolScopeLocked,
	})

	return agent, nil
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
	updated := identity.WithFallback(a.learner)
	changed := updated != a.learner
	a.learner = updated
	a.learnerMu.Unlock()

	if !changed {
		return
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

		// Waiters get context.Canceled from the cancelled context.
		a.pendingMu.Lock()
		a.pendingCalls = make(map[string]chan *plugnmeet.AIAgentToolResultMsg)
		a.pendingMu.Unlock()

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
	return a.sendContextText(BuildTopicAnchorMessage(a.boundary, a.guard.TurnCount()))
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
		},
	}
}

func (a *AIAgent) markSpeaking() {
	a.stateMu.Lock()
	changed := !a.state.speaking || a.state.state != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.speaking = true
	a.state.state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.tool = ""
	// Every audio chunk postpones the fallback, so only a real gap ends the turn.
	a.stopSpeakTimerLocked()
	a.speakTimer = time.AfterFunc(aiAgentSpeakingIdle, a.endIdleSpeaking)
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.broadcast(snapshot)
	}
}

// endIdleSpeaking restores LISTENING once the agent audio stream has been silent
// for longer than any gap between chunks, so a trailing delta or a missing
// turn-complete cannot leave the status stuck on SPEAKING.
func (a *AIAgent) endIdleSpeaking() {
	if a.paused.Load() {
		return
	}

	a.stateMu.Lock()
	speaking := a.state.state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.stateMu.Unlock()
	if !speaking {
		return
	}

	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
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
	changed := a.state.state != state || a.state.tool != tool || a.state.reason != ""
	a.state.state = state
	a.state.speaking = false
	a.state.tool = tool
	a.state.reason = ""
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.logger.WithFields(logrus.Fields{"state": state.String(), "tool": tool, "reason": ""}).Debug("ai agent: state changed")
	}
	a.broadcast(snapshot)
}

// restoreListening returns to LISTENING unless the agent is paused or speaking.
func (a *AIAgent) restoreListening() {
	if a.paused.Load() {
		return
	}
	a.stateMu.Lock()
	speaking := a.state.speaking
	a.stateMu.Unlock()
	if speaking {
		return
	}
	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
}

func (a *AIAgent) broadcast(snapshot aiAgentRuntimeState) {
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
	})
	if err != nil {
		a.logger.WithError(err).Debug("ai agent: failed to broadcast the agent state")
	}
}
