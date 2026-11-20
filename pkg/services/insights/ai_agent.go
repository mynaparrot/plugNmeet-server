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

	transcriptRoleParticipant = "participant"
	transcriptRoleAgent       = "agent"
)

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

	roomAgent *RoomAgent
	publisher audioPublisher

	inAudio chan mediasdk.PCM16Sample

	sessionMu  sync.RWMutex
	session    insights.RealtimeSession
	newSession func(ctx context.Context) (insights.RealtimeSession, error)

	loopMu sync.Mutex
	loop   *aiAgentLoop

	stateMu sync.Mutex
	state   aiAgentRuntimeState

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
	Logger          *logrus.Entry
	Payload         *insights.InsightsTaskPayload
	Features        *plugnmeet.InsightsAIAgentFeatures
	Boundary        BoundaryConfig
	Deps            mcp.ToolDependencies
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

	log := loggerOrDefault(args.Logger).WithFields(logrus.Fields{
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

	agent, err := newAIAgent(&aiAgentCoreArgs{
		Ctx:            args.Ctx,
		Payload:        args.Payload,
		Features:       args.Features,
		Boundary:       boundary,
		Session:        session,
		NewSession:     newSession,
		Transport:      args.Transport,
		RedisService:   args.RedisService,
		Logger:         log,
		Registry:       registry,
		Delegate:       args.Delegate,
		OnStopped:      args.OnStopped,
		AgentUserId:    args.AgentUserId,
		Learner:        args.Learner,
		MaxAudioMinute: args.Features.GetMaxAudioMinutes(),
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
	log := loggerOrDefault(args.Logger).WithFields(logrus.Fields{
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
		ctx:          ctx,
		cancel:       cancel,
		logger:       log,
		roomId:       args.Payload.RoomId,
		sessionId:    sessionId,
		agentUserId:  agentUserId,
		agentName:    boundary.AgentDisplayName,
		startedAt:    time.Now(),
		boundary:     boundary,
		features:     features,
		guard:        guard,
		transport:    args.Transport,
		delegate:     args.Delegate,
		onStopped:    args.OnStopped,
		learner:      args.Learner,
		maxAudio:     time.Duration(maxAudioMinutes) * time.Minute,
		redisService: args.RedisService,
		inAudio:      make(chan mediasdk.PCM16Sample, aiAgentInAudioBuffer),
		session:      args.Session,
		newSession:   args.NewSession,
		pendingCalls: make(map[string]chan *plugnmeet.AIAgentToolResultMsg),
		done:         make(chan struct{}),
		state: aiAgentRuntimeState{
			state:     plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_CONNECTING,
			toolsUsed: make(map[string]int),
		},
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

func loggerOrDefault(log *logrus.Entry) *logrus.Entry {
	if log != nil {
		return log
	}
	return logrus.NewEntry(logrus.StandardLogger())
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
	snapshot := a.state
	a.stateMu.Unlock()

	if changed {
		a.broadcast(snapshot)
	}
}

func (a *AIAgent) setState(state plugnmeet.AIAgentRuntimeState, reason string) {
	a.stateMu.Lock()
	a.state.state = state
	a.state.speaking = state == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	a.state.tool = ""
	a.state.reason = reason
	snapshot := a.state
	a.stateMu.Unlock()

	a.broadcast(snapshot)
}

func (a *AIAgent) setToolState(state plugnmeet.AIAgentRuntimeState, tool string) {
	a.stateMu.Lock()
	a.state.state = state
	a.state.speaking = false
	a.state.tool = tool
	a.state.reason = ""
	snapshot := a.state
	a.stateMu.Unlock()

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
