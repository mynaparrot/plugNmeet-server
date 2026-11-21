package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const (
	aiAgentKeyState         = "state"
	aiAgentKeyDelegate      = "delegate"
	aiAgentKeyPrevPresenter = "prev_presenter"

	aiAgentLockTTL           = 30 * time.Second
	aiAgentLockRefresh       = 15 * time.Second
	aiAgentEmptyRoomGrace    = 20 * time.Second
	aiAgentCleanupTimeout    = 10 * time.Second
	aiAgentPollContextBudget = 5 * time.Second
	// aiAgentRoomCloseTimeout covers writing the session summary and ending the
	// breakout room of a finished 1-on-1 session.
	aiAgentRoomCloseTimeout = 2 * time.Minute

	// Stop reasons that end a per-user 1-on-1 session for good.
	aiAgentStopReasonEmptyRoom = "empty_room"
	aiAgentStopReasonBudget    = "budget"

	// Feature defaults applied when the host left a numeric field unset.
	aiAgentDefaultDisplayName       = "AI Assistant"
	aiAgentDefaultMaxSessionSecond  = 1800
	aiAgentDefaultMaxAudioMinutes   = 120
	aiAgentDefaultVisionDebounceMs  = 1200
	aiAgentDefaultVisionMaxEdgePx   = 1024
	aiAgentDefaultAnchorTurns       = 6
	aiAgentDefaultKnowledgeCacheTtl = 3600
	aiAgentDefaultSpeakingRate      = 1.0

	aiAgentMinVisionDebounceMs  = 500
	aiAgentMaxVisionDebounceMs  = 5000
	aiAgentMinVisionMaxEdgePx   = 512
	aiAgentMaxVisionMaxEdgePx   = 1600
	aiAgentMinAnchorTurns       = 3
	aiAgentMaxAnchorTurns       = 12
	aiAgentMinKnowledgeCacheTtl = 60
	aiAgentMaxKnowledgeCacheTtl = 86400
	aiAgentMaxSessionSecondsCap = 7200

	// aiAgentMaxAllowedParticipantIds caps the pre-authorized participant list.
	// It matches the max_items=32 of auto_session_config.allowed_participant_ids
	// in the protocol.
	aiAgentMaxAllowedParticipantIds = 32

	// Shape limits of one external MCP server entry.
	agentMCPMaxKeyChars         = 64
	agentMCPMaxURLChars         = 1024
	agentMCPMaxAllowedTools     = 8
	agentMCPMaxAllowedToolChars = 128

	// Shape limits of the host-supplied custom provider (BYOK/BYOP).
	aiAgentCustomProviderGoogle      = "google"
	aiAgentCustomProviderOpenAI      = "openai"
	aiAgentCustomProviderMaxProvider = 32
	aiAgentCustomProviderMaxAPIKey   = 512
	aiAgentCustomProviderMaxEndpoint = 512
	aiAgentCustomProviderMaxHeader   = 128
	aiAgentCustomProviderMaxModel    = 128

	// Model defaults of a custom provider, used when the host set no override
	// and the server config has no ai_agent service for the same provider.
	aiAgentCustomGoogleLiveModel = "gemini-3.1-flash-live-preview"
	aiAgentCustomGoogleTextModel = "gemini-3.1-flash-lite-preview"
	aiAgentCustomOpenAILiveModel = "gpt-realtime-1.5"
	aiAgentCustomOpenAITextModel = "gpt-5.4-mini"
)

// aiAgentForbiddenAuthHeaders are the headers a custom provider key must never be
// injected into: request-shaping and hop-by-hop headers belong to the transport.
var aiAgentForbiddenAuthHeaders = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
	"upgrade":           {},
	"cookie":            {},
	"keep-alive":        {},
	"te":                {},
	"trailer":           {},
	"proxy-connection":  {},
}

// Client API routes are submitted by the Admin's browser through the
// client_api_submit tool so that the request is validated by the auth router.
const (
	clientAPISubmitToolName = "client_api_submit"
	clientAPISubmitTimeout  = 25 * time.Second

	clientAPIRouteBreakoutCreate                = "breakoutRoom/create"
	clientAPIRouteTranscriptionConfigure        = "insights/transcription/configure"
	clientAPIRouteTranscriptionEnd              = "insights/transcription/end"
	clientAPIRouteChatTranslationConfigure      = "insights/translation/chat/configure"
	clientAPIRouteChatTranslationEnd            = "insights/translation/chat/end"
	clientAPIRouteMeetingSummarizationConfigure = "insights/ai/meetingSummarization/configure"
	clientAPIRouteMeetingSummarizationEnd       = "insights/ai/meetingSummarization/end"
	clientAPIRouteRecording                     = "recording"
)

// aiAgentRoomState is the room-scoped session state stored in Redis so any
// server in the fleet can answer status questions.
type aiAgentRoomState struct {
	SessionId   string                        `json:"session_id"`
	AgentUserId string                        `json:"agent_user_id"`
	AgentName   string                        `json:"agent_name"`
	StartedAt   int64                         `json:"started_at"`
	State       plugnmeet.AIAgentRuntimeState `json:"state"`
	Reason      string                        `json:"reason,omitempty"`
}

// AIAgentModel owns the AI agent lifecycle of a room: activation, delegate
// selection, presenter handover, teardown and the tool backends.
type AIAgentModel struct {
	ctx          context.Context
	appConfig    *config.AppConfig
	rds          redis.UniversalClient
	natsConn     *nats.Conn
	redisService *redisservice.RedisService
	natsService  *natsservice.NatsService
	pollModel    *PollModel
	userModel    *UserModel
	logger       *logrus.Entry

	// Wired through initializers (see wireCircularModels): RoomModel already
	// depends on AIAgentModel, so the reverse references cannot go through Fx.
	roomModel      *RoomModel
	bkRoomModel    *BreakoutRoomModel
	recordingModel *RecordingModel
	insightsModel  *InsightsModel
	artifactModel  *ArtifactModel

	timerMu    sync.Mutex
	stopTimers map[string]*time.Timer

	// pendingClientCalls holds the tool calls delegated to a participant's
	// browser outside an agent session, e.g. the admin AI text chat. The agent's
	// own calls live in the services layer session.
	pendingClientMu    sync.Mutex
	pendingClientCalls map[string]*pendingClientToolCall

	// clientAPISubmitter overrides the browser round-trip in unit tests.
	clientAPISubmitter func(ctx context.Context, roomID, targetUserID, route string, payload proto.Message) error
	// activeAgentLookup overrides the hosted-agent registry in unit tests.
	activeAgentLookup func(roomID string) (aiAgentHandle, bool)
	// closeBreakoutRoom overrides the 1-on-1 room teardown in unit tests.
	closeBreakoutRoom func(roomID, reason string)
}

// aiAgentHandle is the slice of insightsservice.AIAgent the model needs: push a
// server-side note into the live session and stop it.
type aiAgentHandle interface {
	SendProactiveContext(text, source string) error
	Stop(reason string)
}

// hostedAgent returns the agent this process hosts for the room, if any.
func (m *AIAgentModel) hostedAgent(roomID string) (aiAgentHandle, bool) {
	if m.activeAgentLookup != nil {
		return m.activeAgentLookup(roomID)
	}
	agent, ok := insightsservice.GetActiveAIAgent(roomID)
	if !ok {
		return nil, false
	}

	return agent, true
}

// pendingClientToolCall is one client tool call waiting for the browser of
// exactly one participant in exactly one room to answer it.
type pendingClientToolCall struct {
	roomID       string
	targetUserID string
	ch           chan *plugnmeet.AIAgentToolResultMsg
}

type AIAgentModelArgs struct {
	fx.In
	Ctx          context.Context
	AppConfig    *config.AppConfig
	RDS          redis.UniversalClient
	NatsConn     *nats.Conn
	RedisService *redisservice.RedisService
	NatsService  *natsservice.NatsService
	PollModel    *PollModel
	UserModel    *UserModel
	Logger       *logrus.Logger
}

func NewAIAgentModel(args AIAgentModelArgs) *AIAgentModel {
	m := &AIAgentModel{
		ctx:          args.Ctx,
		appConfig:    args.AppConfig,
		rds:          args.RDS,
		natsConn:     args.NatsConn,
		redisService: args.RedisService,
		natsService:  args.NatsService,
		pollModel:    args.PollModel,
		userModel:    args.UserModel,
		logger:       args.Logger.WithField("model", "aiAgent"),
		stopTimers:   make(map[string]*time.Timer),

		pendingClientCalls: make(map[string]*pendingClientToolCall),
	}

	return m
}

// userClientRPCSender delegates every tool call to one participant's browser.
type userClientRPCSender struct {
	model        *AIAgentModel
	targetUserID string
}

func (s userClientRPCSender) CallClientTool(ctx context.Context, call mcp.ToolCall, timeout time.Duration) (mcp.ToolResult, error) {
	return s.model.CallClientToolForUser(ctx, s.targetUserID, call, timeout)
}

// NewUserClientRPCSender returns the mcp.ClientRPCSender that a tool executor
// uses to reach targetUserID's browser, e.g. the administrator who runs an AI
// text chat session.
func (m *AIAgentModel) NewUserClientRPCSender(targetUserID string) mcp.ClientRPCSender {
	return userClientRPCSender{model: m, targetUserID: targetUserID}
}

// CallClientToolForUser delegates one tool call to a specific participant's
// browser and waits for its answer. It mirrors AIAgent.CallClientTool, but the
// target is a user id instead of the agent's current delegate.
func (m *AIAgentModel) CallClientToolForUser(ctx context.Context, targetUserID string, call mcp.ToolCall, timeout time.Duration) (mcp.ToolResult, error) {
	if targetUserID == "" || call.RoomID == "" {
		return mcp.ToolResult{}, errors.New("no client is available to run this tool")
	}

	callID := strings.TrimSpace(call.CallID)
	if callID == "" {
		callID = uuid.NewString()
		call.CallID = callID
	}

	ch := make(chan *plugnmeet.AIAgentToolResultMsg, 1)
	m.pendingClientMu.Lock()
	if m.pendingClientCalls == nil {
		m.pendingClientCalls = make(map[string]*pendingClientToolCall)
	}
	m.pendingClientCalls[callID] = &pendingClientToolCall{roomID: call.RoomID, targetUserID: targetUserID, ch: ch}
	m.pendingClientMu.Unlock()
	defer func() {
		m.pendingClientMu.Lock()
		delete(m.pendingClientCalls, callID)
		m.pendingClientMu.Unlock()
	}()

	msg := &plugnmeet.AIAgentToolCallMsg{
		CallId:         callID,
		ToolName:       call.ToolName,
		ArgumentsJson:  call.ArgsJSON,
		TargetUserId:   targetUserID,
		DeadlineUnixMs: time.Now().Add(timeout).UnixMilli(),
		TraceId:        call.TraceID,
	}
	if err := m.sendClientToolCall(call.RoomID, targetUserID, msg); err != nil {
		return mcp.ToolResult{}, fmt.Errorf("the tool call could not be delivered: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		if !res.GetOk() {
			reason := strings.TrimSpace(res.GetError())
			if reason == "" {
				reason = "the participant's browser could not run the tool"
			}
			return mcp.ToolResult{}, errors.New(reason)
		}
		out := strings.TrimSpace(res.GetResultJson())
		if out == "" {
			out = "{}"
		}
		return mcp.ToolResult{OutputJSON: out}, nil
	case <-ctx.Done():
		return mcp.ToolResult{}, ctx.Err()
	case <-timer.C:
		return mcp.ToolResult{}, context.DeadlineExceeded
	}
}

// sendClientToolCall delivers the call to the participant's browser.
func (m *AIAgentModel) sendClientToolCall(roomId, targetUserID string, msg *plugnmeet.AIAgentToolCallMsg) error {
	return m.natsService.SendAIAgentToolCallToUser(roomId, targetUserID, msg)
}

// takePendingClientCall removes the pending call callID when it belongs to
// roomId and userId, so an answer from another participant or room can never
// resolve it.
func (m *AIAgentModel) takePendingClientCall(callID, roomId, userId string) *pendingClientToolCall {
	if callID == "" {
		return nil
	}

	m.pendingClientMu.Lock()
	defer m.pendingClientMu.Unlock()
	entry, ok := m.pendingClientCalls[callID]
	if !ok || entry.roomID != roomId || entry.targetUserID != userId {
		return nil
	}
	delete(m.pendingClientCalls, callID)

	return entry
}

// submitClientAPI hands a server-built protobuf request to the Admin's browser,
// which submits it through the same HTTP endpoint the UI uses.
func (m *AIAgentModel) submitClientAPI(ctx context.Context, roomID, actorUserID, route string, payload proto.Message) error {
	argsJSON, err := buildClientAPISubmitArgs(route, payload)
	if err != nil {
		return err
	}

	targetUserID := strings.TrimSpace(actorUserID)
	if targetUserID == "" || config.IsUserIdInternal(targetUserID) {
		targetUserID, err = m.SelectOrGetDelegate(ctx, roomID)
		if err != nil {
			return err
		}
	}
	if targetUserID == "" {
		return errors.New("no administrator client is available to submit this request")
	}

	if m.clientAPISubmitter != nil {
		return m.clientAPISubmitter(ctx, roomID, targetUserID, route, payload)
	}

	_, err = m.CallClientToolForUser(ctx, targetUserID, mcp.ToolCall{
		RoomID:   roomID,
		ToolName: clientAPISubmitToolName,
		ArgsJSON: argsJSON,
	}, clientAPISubmitTimeout)
	return err
}

// buildClientAPISubmitArgs renders the client_api_submit arguments; a nil
// payload means the route takes no request body.
func buildClientAPISubmitArgs(route string, payload proto.Message) (string, error) {
	args := map[string]any{"route": route}
	if payload != nil {
		raw, err := protojson.Marshal(payload)
		if err != nil {
			return "", err
		}
		args["payload"] = json.RawMessage(raw)
	}

	encoded, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (m *AIAgentModel) SetRoomModel(rm *RoomModel) {
	m.roomModel = rm
}

func (m *AIAgentModel) SetBreakoutRoomModel(bm *BreakoutRoomModel) {
	m.bkRoomModel = bm
}

func (m *AIAgentModel) SetRecordingModel(rm *RecordingModel) {
	m.recordingModel = rm
}

func (m *AIAgentModel) SetInsightsModel(im *InsightsModel) {
	m.insightsModel = im
}

func (m *AIAgentModel) SetArtifactModel(am *ArtifactModel) {
	m.artifactModel = am
}

// BuildToolDependencies wires every MCP tool backend of a room session.
// actorUserID is the participant the tools act as; it is used when a tool call
// carries no user of its own. The caller sets MCPScope (and
// RequireQuizApproval for a text chat session) because the same backends serve
// the voice agent and the AI text chat.
func (m *AIAgentModel) BuildToolDependencies(roomID, actorUserID string, log *logrus.Entry) mcp.ToolDependencies {
	return mcp.ToolDependencies{
		Poll:         aiAgentPollBackend{m: m},
		Quiz:         m.redisService,
		Chat:         aiAgentChatBackend{m: m},
		Room:         aiAgentRoomBackend{m: m},
		Progress:     m.redisService,
		QuizDrafts:   m.redisService,
		Notepad:      aiAgentNotepadBackend{nats: m.natsService},
		Whiteboard:   aiAgentWhiteboardBackend{nats: m.natsService},
		Cache:        m.redisService,
		Breakout:     aiAgentBreakoutBackend{m: m, actorUserID: actorUserID},
		Insights:     aiAgentInsightsBackend{m: m, actorUserID: actorUserID},
		RoomOps:      aiAgentRoomOpsBackend{m: m, actorUserID: actorUserID},
		Participants: aiAgentParticipantsBackend{m: m, actorUserID: actorUserID},
		RedisService: m.redisService,
		RoomID:       roomID,
		Logger:       log,
	}
}

func aiAgentKey(roomId, suffix string) string {
	return fmt.Sprintf("pnm:ai_agent:%s:%s", roomId, suffix)
}

func aiAgentLockKey(roomId string) string {
	return fmt.Sprintf("%s@%s", roomId, insights.ServiceTypeAIAgent)
}

// NormalizeAIAgentFeatures returns a copy of f with every documented default
// applied. The room's stored metadata is never mutated.
func NormalizeAIAgentFeatures(f *plugnmeet.InsightsAIAgentFeatures) *plugnmeet.InsightsAIAgentFeatures {
	if f == nil {
		return nil
	}

	out := proto.Clone(f).(*plugnmeet.InsightsAIAgentFeatures)

	displayName := strings.TrimSpace(out.GetAgentDisplayName())
	if displayName == "" {
		displayName = aiAgentDefaultDisplayName
	}
	out.AgentDisplayName = &displayName

	out.Topic = truncate(trimSpace(out.Topic), 200)
	// A topic that is both allowed and forbidden is forbidden: the narrower rule wins.
	out.ForbiddenTopics = cleanAgentList(out.ForbiddenTopics, 160)
	out.AllowedSubtopics = cleanAgentList(out.AllowedSubtopics, 160, out.ForbiddenTopics...)

	if out.GetVisionDebounceMs() == 0 {
		out.VisionDebounceMs = aiAgentDefaultVisionDebounceMs
	}
	out.VisionDebounceMs = clampUint32(out.VisionDebounceMs, aiAgentMinVisionDebounceMs, aiAgentMaxVisionDebounceMs)

	if out.GetVisionMaxEdgePx() == 0 {
		out.VisionMaxEdgePx = aiAgentDefaultVisionMaxEdgePx
	}
	out.VisionMaxEdgePx = clampUint32(out.VisionMaxEdgePx, aiAgentMinVisionMaxEdgePx, aiAgentMaxVisionMaxEdgePx)

	if out.GetTopicAnchorIntervalTurns() == 0 {
		out.TopicAnchorIntervalTurns = aiAgentDefaultAnchorTurns
	}
	out.TopicAnchorIntervalTurns = clampUint32(out.TopicAnchorIntervalTurns, aiAgentMinAnchorTurns, aiAgentMaxAnchorTurns)

	if out.GetMaxSessionSeconds() == 0 {
		out.MaxSessionSeconds = aiAgentDefaultMaxSessionSecond
	}
	out.MaxSessionSeconds = clampUint32(out.MaxSessionSeconds, 60, aiAgentMaxSessionSecondsCap)

	if out.GetMaxAudioMinutes() == 0 {
		out.MaxAudioMinutes = aiAgentDefaultMaxAudioMinutes
	}

	if out.GetKnowledgeCacheTtlSeconds() == 0 {
		out.KnowledgeCacheTtlSeconds = aiAgentDefaultKnowledgeCacheTtl
	}
	out.KnowledgeCacheTtlSeconds = clampUint32(out.KnowledgeCacheTtlSeconds, aiAgentMinKnowledgeCacheTtl, aiAgentMaxKnowledgeCacheTtl)

	if out.GetSpeakingRate() == 0 {
		rate := float32(aiAgentDefaultSpeakingRate)
		out.SpeakingRate = &rate
	}

	out.McpServers = NormalizeExternalMCPServers(out.GetMcpServers())
	out.CustomProvider = NormalizeAIAgentCustomProvider(out.GetCustomProvider())

	// the session routing config is host input, the rest is runtime state. Only
	// the fields of the selected mode survive, so a room that switched modes
	// never keeps the settings of the previous one.
	normalizeAIAgentAutoSessionConfig(out)
	out.IsEnabled = false
	out.ActiveSessionId = nil

	return out
}

// normalizeAIAgentAutoSessionConfig keeps only the auto session config that
// belongs to the selected session mode and applies its documented defaults.
func normalizeAIAgentAutoSessionConfig(out *plugnmeet.InsightsAIAgentFeatures) {
	switch out.GetSessionMode() {
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS:
		if out.AutoSessionConfig == nil {
			out.AutoSessionConfig = &plugnmeet.AIAgentAutoSessionConfig{}
		}
		out.AutoSessionConfig.AllowedParticipantIds = cleanAllowedParticipantIds(out.AutoSessionConfig.GetAllowedParticipantIds())
		// a controlled roster is a pre-authorization list, not a concurrency
		// limit: the sessions are capped when they are created
		out.AutoSessionConfig.MaxConcurrentSessions = 0
		if out.AutoSessionConfig.AutoStartAgent == nil {
			out.AutoSessionConfig.AutoStartAgent = new(true)
		}
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER:
		if out.AutoSessionConfig == nil {
			out.AutoSessionConfig = &plugnmeet.AIAgentAutoSessionConfig{}
		}
		out.AutoSessionConfig.AllowedParticipantIds = nil
		// the server opens at most the cap sessions, so a higher host value is
		// clamped instead of refused
		if out.AutoSessionConfig.GetMaxConcurrentSessions() > utils.AIAgentMaxConcurrentSessionsCap {
			out.AutoSessionConfig.MaxConcurrentSessions = utils.AIAgentMaxConcurrentSessionsCap
		}
		if out.AutoSessionConfig.AutoStartAgent == nil {
			out.AutoSessionConfig.AutoStartAgent = new(true)
		}
	default:
		// a normal room routes nothing automatically
		out.AutoSessionConfig = nil
	}
}

// aiAgentAllowedParticipantIds returns the pre-authorized participants of a
// controlled-users room, and nil in every other mode: only that mode has a
// fixed roster.
func aiAgentAllowedParticipantIds(f *plugnmeet.InsightsAIAgentFeatures) []string {
	if f.GetSessionMode() != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS {
		return nil
	}
	return f.GetAutoSessionConfig().GetAllowedParticipantIds()
}

// aiAgentMaxConcurrentSessions returns how many 1-on-1 sessions an
// open-per-user room may run in parallel. 0 means "not configured"; the mode
// that does not open per-user sessions always reports 0.
func aiAgentMaxConcurrentSessions(f *plugnmeet.InsightsAIAgentFeatures) uint32 {
	if f.GetSessionMode() != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER {
		return 0
	}
	return f.GetAutoSessionConfig().GetMaxConcurrentSessions()
}

// aiAgentAutoStart reports whether the agent starts by itself when its session
// room becomes occupied. It defaults to true in both routing modes and has no
// meaning in a normal room, where an admin starts the agent explicitly.
func aiAgentAutoStart(f *plugnmeet.InsightsAIAgentFeatures) bool {
	switch f.GetSessionMode() {
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS,
		plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER:
		if cfg := f.GetAutoSessionConfig(); cfg != nil && cfg.AutoStartAgent != nil {
			return cfg.GetAutoStartAgent()
		}
		return true
	default:
		return false
	}
}

// isAutoPerUserAISessionRoom reports whether every participant gets their own
// 1-on-1 session room: always in open-per-user mode, and in controlled-users
// mode whenever at least one participant is pre-authorized.
func isAutoPerUserAISessionRoom(f *plugnmeet.InsightsAIAgentFeatures) bool {
	if !f.GetIsAllow() {
		return false
	}

	switch f.GetSessionMode() {
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER:
		return true
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS:
		return len(f.GetAutoSessionConfig().GetAllowedParticipantIds()) > 0
	default:
		return false
	}
}

// cleanAllowedParticipantIds trims, dedupes and caps the pre-authorized
// participant list while keeping the host's order.
func cleanAllowedParticipantIds(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		if len(out) >= aiAgentMaxAllowedParticipantIds {
			break
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// containsUserId reports whether userId is part of ids.
func containsUserId(ids []string, userId string) bool {
	for _, id := range ids {
		if id == userId {
			return true
		}
	}
	return false
}

// NormalizeExternalMCPServers trims and caps the host's external MCP servers. It
// is shared by the AI agent and the AI text chat features. The servers are kept
// in a deterministic order so the applied cap never depends on the proto map's
// random iteration order.
func NormalizeExternalMCPServers(servers map[string]*plugnmeet.ExternalMCPServerConfig) map[string]*plugnmeet.ExternalMCPServerConfig {
	if len(servers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(servers))
	for key, cfg := range servers {
		if cfg == nil || strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]*plugnmeet.ExternalMCPServerConfig, len(keys))
	for _, rawKey := range keys {
		if len(out) >= mcp.MaxExternalMCPServersPerRoom {
			break
		}
		cfg := servers[rawKey]
		key := truncate(strings.TrimSpace(rawKey), agentMCPMaxKeyChars)
		endpoint := truncate(strings.TrimSpace(cfg.GetUrl()), agentMCPMaxURLChars)
		if key == "" || endpoint == "" {
			continue
		}
		tools := cleanAgentList(cfg.GetAllowedTools(), agentMCPMaxAllowedToolChars)
		if len(tools) > agentMCPMaxAllowedTools {
			tools = tools[:agentMCPMaxAllowedTools]
		}
		out[key] = &plugnmeet.ExternalMCPServerConfig{
			Url:          endpoint,
			Headers:      mcp.SanitizeExternalMCPHeaders(cfg.GetHeaders()),
			AllowedTools: tools,
			Scope:        trimmedScope(cfg.GetScope()),
		}
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

// NormalizeAIAgentCustomProvider trims the host-supplied provider credentials to
// their documented shape. An optional field left empty is dropped so the server
// defaults apply, and every secret is capped before it reaches Redis.
func NormalizeAIAgentCustomProvider(in *plugnmeet.InsightsAIAgentCustomProvider) *plugnmeet.InsightsAIAgentCustomProvider {
	if in == nil {
		return nil
	}

	out := proto.Clone(in).(*plugnmeet.InsightsAIAgentCustomProvider)
	out.Provider = truncate(strings.ToLower(strings.TrimSpace(out.GetProvider())), aiAgentCustomProviderMaxProvider)
	out.ApiKey = truncate(strings.TrimSpace(out.GetApiKey()), aiAgentCustomProviderMaxAPIKey)
	out.Endpoint = optionalTrimmedString(out.GetEndpoint(), aiAgentCustomProviderMaxEndpoint)
	out.AuthHeader = optionalTrimmedString(out.GetAuthHeader(), aiAgentCustomProviderMaxHeader)
	out.RealtimeModel = optionalTrimmedString(out.GetRealtimeModel(), aiAgentCustomProviderMaxModel)
	out.DelegationModel = optionalTrimmedString(out.GetDelegationModel(), aiAgentCustomProviderMaxModel)
	out.SummarizeModel = optionalTrimmedString(out.GetSummarizeModel(), aiAgentCustomProviderMaxModel)

	return out
}

// optionalTrimmedString trims and caps an optional string, and returns nil when
// nothing is left so the proto field is omitted instead of sent empty.
func optionalTrimmedString(value string, max int) *string {
	value = truncate(strings.TrimSpace(value), max)
	if value == "" {
		return nil
	}
	return &value
}

// validateAIAgentCustomProvider enforces what a usable custom provider needs: a
// known provider, a clean API key, a safe gateway endpoint, a header name the
// key may travel in, and a delegation model for a gpt-live realtime model, which
// cannot answer a turn on its own.
func validateAIAgentCustomProvider(cp *plugnmeet.InsightsAIAgentCustomProvider) error {
	provider := strings.ToLower(strings.TrimSpace(cp.GetProvider()))
	switch provider {
	case aiAgentCustomProviderGoogle, aiAgentCustomProviderOpenAI:
	default:
		return config.ErrAIAgentInvalidRequest
	}

	apiKey := strings.TrimSpace(cp.GetApiKey())
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return config.ErrAIAgentInvalidRequest
	}

	if endpoint := strings.TrimSpace(cp.GetEndpoint()); endpoint != "" && !isAllowedAgentCustomProviderEndpoint(endpoint) {
		return config.ErrAIAgentInvalidRequest
	}
	if header := strings.TrimSpace(cp.GetAuthHeader()); header != "" && !isAllowedAgentCustomProviderAuthHeader(header) {
		return config.ErrAIAgentInvalidRequest
	}

	// A gpt-live session delegates every text turn to another model. The
	// ephemeral service config always carries the provider's default delegation
	// model, so nothing is missing unless the host overrode every source of one.
	if provider == aiAgentCustomProviderOpenAI &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(cp.GetRealtimeModel())), "gpt-live") &&
		strings.TrimSpace(cp.GetDelegationModel()) == "" &&
		strings.TrimSpace(aiAgentCustomProviderModelDefaults(config.ProviderOpenAI)["delegation_model"]) == "" {
		return config.ErrAIAgentInvalidRequest
	}

	return nil
}

// isAllowedAgentCustomProviderEndpoint checks the shape of a custom gateway
// endpoint: https, no credentials, no query and no fragment, a valid port, and no
// obviously internal IP literal. Like the external MCP endpoints, the DNS lookup
// is left to the dialer so creating a room never waits on a resolver.
func isAllowedAgentCustomProviderEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.RawQuery != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	if ip := net.ParseIP(host); ip != nil && mcp.IsBlockedExternalIP(ip) {
		return false
	}

	return true
}

// isAllowedAgentCustomProviderAuthHeader accepts a plain HTTP header token name
// that may carry the API key. Pseudo headers and the request-shaping or
// hop-by-hop headers a transport owns are refused.
func isAllowedAgentCustomProviderAuthHeader(header string) bool {
	if header == "" || len(header) > aiAgentCustomProviderMaxHeader {
		return false
	}
	for i := 0; i < len(header); i++ {
		c := header[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	if _, forbidden := aiAgentForbiddenAuthHeaders[strings.ToLower(header)]; forbidden {
		return false
	}

	return true
}

// ValidateAIAgentFeatures enforces the activation rules that must hold both when
// a room is created and when the agent is started manually.
func (m *AIAgentModel) ValidateAIAgentFeatures(f *plugnmeet.InsightsAIAgentFeatures, e2ee *plugnmeet.EndToEndEncryptionFeatures, insightsAllowed bool) error {
	return m.validateAIAgentFeatures(f, e2ee, insightsAllowed, false)
}

// validateAIAgentFeatures is the shared validator of a room's agent features.
// hasRoomProvider reports that the room manages its own provider server-side,
// which is always the case at start time: the credentials are stripped from the
// metadata during room creation, so they can never be part of f.
func (m *AIAgentModel) validateAIAgentFeatures(f *plugnmeet.InsightsAIAgentFeatures, e2ee *plugnmeet.EndToEndEncryptionFeatures, insightsAllowed, hasRoomProvider bool) error {
	if f == nil || !f.GetIsAllow() {
		return config.ErrAIAgentNotAllowed
	}
	if !insightsAllowed {
		return config.ErrAIAgentNotAllowed
	}
	// A room-managed key is fine (the agent receives the key), a self-inserted
	// per-device key can never be read by the server.
	if e2ee.GetIsEnabled() && e2ee.GetEnabledSelfInsertEncryptionKey() {
		return config.ErrAIAgentE2EEUnsupported
	}
	if strings.TrimSpace(f.GetSystemPrompt()) == "" {
		return config.ErrAIAgentPromptRequired
	}
	if strings.TrimSpace(f.GetTopic()) == "" {
		return config.ErrAIAgentTopicRequired
	}
	// a room that brings its own provider does not need the server-configured
	// ai_agent service at all
	if cp := f.GetCustomProvider(); cp != nil {
		if err := validateAIAgentCustomProvider(cp); err != nil {
			return err
		}
	} else if !hasRoomProvider {
		if m.appConfig.Insights == nil {
			return config.ErrAIAgentProviderMissing
		}
		if _, _, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent); err != nil {
			return config.ErrAIAgentProviderMissing
		}
	}
	if err := ValidateExternalMCPServers(f.GetMcpServers()); err != nil {
		return err
	}

	return ValidateAIAgentSessionMode(f)
}

// ValidateAIAgentSessionMode enforces the session routing rules: the mode must be
// known, and the auto session config it needs must be usable.
func ValidateAIAgentSessionMode(f *plugnmeet.InsightsAIAgentFeatures) error {
	switch f.GetSessionMode() {
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM:
		return nil
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS:
		if len(cleanAllowedParticipantIds(f.GetAutoSessionConfig().GetAllowedParticipantIds())) == 0 {
			return config.ErrAIAgentAllowedUsersRequired
		}
		return nil
	case plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER:
		// a value above the cap is clamped, only a missing one is refused
		if aiAgentMaxConcurrentSessions(f) == 0 {
			return config.ErrAIAgentMaxConcurrentSessions
		}
		return nil
	default:
		// a mode this server does not know can never be routed
		return config.ErrAIAgentInvalidRequest
	}
}

// ValidateExternalMCPServers only checks the shape of the endpoints: https, port
// 443, no credentials, no fragment and no obviously internal IP literal. It is
// shared by the AI agent and the AI text chat features. The DNS lookup and the
// dial-time address checks happen in the MCP client, so creating a room never
// waits on a slow resolver.
func ValidateExternalMCPServers(servers map[string]*plugnmeet.ExternalMCPServerConfig) error {
	if len(servers) > mcp.MaxExternalMCPServersPerRoom {
		return config.ErrAIAgentInvalidMCPServer
	}
	for key, cfg := range servers {
		if strings.TrimSpace(key) == "" || cfg == nil || !isAllowedAgentMCPEndpoint(cfg.GetUrl()) {
			return config.ErrAIAgentInvalidMCPServer
		}
	}

	return nil
}

func isAllowedAgentMCPEndpoint(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	host := parsed.Hostname()
	if host == "" {
		return false
	}
	// Internal services listen on their own ports, so https is pinned to 443.
	if port := parsed.Port(); port != "" && port != "443" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && mcp.IsBlockedExternalIP(ip) {
		return false
	}

	return true
}

// trimmedScope returns the host-provided scope without surrounding blanks, or
// nil when it left the field unset (the wiring layer then stamps the owning
// feature's scope).
func trimmedScope(scope string) *string {
	value := strings.TrimSpace(scope)
	if value == "" {
		return nil
	}
	return &value
}

// MergeExternalMCPServers stamps the AI agent's and the AI text chat's servers
// with the scope of the feature that configured them and returns the single
// room configuration. The endpoints themselves are validated separately
// (ValidateExternalMCPServers), so a room whose stored metadata is already
// trusted keeps working. The merged set must fit the room budget: both features
// share at most mcp.MaxExternalMCPServersPerRoom servers. The same key
// configured with the same endpoint by both features becomes one shared entry;
// the same key with different endpoints is a configuration error.
func MergeExternalMCPServers(agent, textChat map[string]*plugnmeet.ExternalMCPServerConfig) (map[string]*plugnmeet.ExternalMCPServerConfig, error) {
	if len(agent) == 0 && len(textChat) == 0 {
		return nil, nil
	}

	merged := make(map[string]*plugnmeet.ExternalMCPServerConfig, len(agent)+len(textChat))
	add := func(servers map[string]*plugnmeet.ExternalMCPServerConfig, scope string) error {
		for _, rawKey := range sortedMCPServerKeys(servers) {
			cfg := servers[rawKey]
			key := truncate(strings.TrimSpace(rawKey), agentMCPMaxKeyChars)
			endpoint := truncate(strings.TrimSpace(cfg.GetUrl()), agentMCPMaxURLChars)
			if key == "" || endpoint == "" {
				return config.ErrAIAgentInvalidMCPServer
			}

			if existing, ok := merged[key]; ok {
				if strings.TrimSpace(existing.GetUrl()) != endpoint {
					// The two features must not claim one key for different
					// endpoints: the tool names would be ambiguous.
					return config.ErrAIAgentInvalidMCPServer
				}
				// One endpoint serves both features, so it is shared.
				shared := mcp.MCPScopeShared
				existing.Scope = &shared
				continue
			}
			if len(merged) >= mcp.MaxExternalMCPServersPerRoom {
				return config.ErrAIAgentInvalidMCPServer
			}

			serverScope := scope
			if own := strings.TrimSpace(cfg.GetScope()); own != "" {
				serverScope = own
			}
			merged[key] = &plugnmeet.ExternalMCPServerConfig{
				Url:          endpoint,
				Headers:      mcp.SanitizeExternalMCPHeaders(cfg.GetHeaders()),
				AllowedTools: cleanAgentList(cfg.GetAllowedTools(), agentMCPMaxAllowedToolChars),
				Scope:        &serverScope,
			}
		}
		return nil
	}

	if err := add(agent, mcp.MCPScopeAIAgent); err != nil {
		return nil, err
	}
	if err := add(textChat, mcp.MCPScopeTextChat); err != nil {
		return nil, err
	}
	if len(merged) == 0 {
		return nil, nil
	}

	return merged, nil
}

// sortedMCPServerKeys keeps the merge deterministic: the applied cap must never
// depend on the proto map's random iteration order.
func sortedMCPServerKeys(servers map[string]*plugnmeet.ExternalMCPServerConfig) []string {
	keys := make([]string, 0, len(servers))
	for key, cfg := range servers {
		if cfg == nil {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// PrepareRoomExternalMCP moves the room's external MCP servers out of the room
// metadata, which is broadcast to every participant, and pre-warms their
// read-only tools in Redis. It must run after the features are validated and
// before the metadata is published. The optional text chat features carry the
// servers of the AI text chat, which share the room's server budget.
func (m *AIAgentModel) PrepareRoomExternalMCP(ctx context.Context, roomId string, agentFeatures *plugnmeet.InsightsAIAgentFeatures, textChatFeatures ...*plugnmeet.InsightsAITextChatFeatures) {
	var chatFeatures *plugnmeet.InsightsAITextChatFeatures
	if len(textChatFeatures) > 0 {
		chatFeatures = textChatFeatures[0]
	}
	if agentFeatures == nil && chatFeatures == nil {
		return
	}

	agentServers := agentFeatures.GetMcpServers()
	textChatServers := chatFeatures.GetMcpServers()
	// Credentials never travel with the metadata, so they cannot leak through
	// NATS to the browsers. Neither feature set keeps them: the servers are only
	// accepted while creating the room, never hydrated back into the metadata.
	agentFeatures.McpServers = nil
	if chatFeatures != nil {
		chatFeatures.McpServers = nil
	}

	servers, err := MergeExternalMCPServers(agentServers, textChatServers)
	if err != nil {
		m.logger.WithError(err).WithField("room_id", roomId).Warn("external mcp servers were not warmed")
		servers = nil
	}
	if len(servers) == 0 {
		// A room recreated without external servers must not inherit the state
		// of an older session that used the same room id.
		_ = m.redisService.ClearExternalMCPTools(ctx, roomId)
		return
	}

	mcp.WarmRoomExternalMCP(ctx, m.redisService, roomId, servers, agentFeatures.GetMaxSessionSeconds(), m.logger)
}

// isOneOnOneAIAgentRoom reports whether the voice agent may run in this room.
// Every AI session runs inside its own 1-on-1 breakout room, so a group room and
// the parent lobby of per-user sessions are never session rooms.
func isOneOnOneAIAgentRoom(meta *plugnmeet.RoomMetadata) bool {
	return meta.GetIsBreakoutRoom() &&
		meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures().GetIsAllow()
}

// isPerUserAISessionParent reports whether meta describes the main room of an
// auto per-user AI session, whose breakout rooms are created one per
// participant. An unused pre-created session room must not keep that room alive.
func isPerUserAISessionParent(meta *plugnmeet.RoomMetadata) bool {
	if meta.GetIsBreakoutRoom() {
		return false
	}
	return isAutoPerUserAISessionRoom(meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures())
}

// roomAgentFeatures returns the room's normalized AI agent features, or nil when
// the agent is not allowed in this room.
func (m *AIAgentModel) roomAgentFeatures(roomId string) *plugnmeet.InsightsAIAgentFeatures {
	metadata, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil || metadata == nil {
		return nil
	}
	features := metadata.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
	if features == nil || !features.GetIsAllow() {
		return nil
	}

	return NormalizeAIAgentFeatures(features)
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// cleanAgentList trims, truncates and dedupes a list of topics. Entries that also
// appear in blocked are dropped: a forbidden topic always wins.
func cleanAgentList(items []string, maxRunes int, blocked ...string) []string {
	if len(items) == 0 {
		return nil
	}
	blockedSet := make(map[string]struct{}, len(blocked))
	for _, b := range blocked {
		blockedSet[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
	}

	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = truncate(strings.TrimSpace(item), maxRunes)
		if item == "" {
			continue
		}
		if _, ok := blockedSet[strings.ToLower(item)]; ok {
			continue
		}
		if _, ok := seen[strings.ToLower(item)]; ok {
			continue
		}
		seen[strings.ToLower(item)] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}

	return out
}

func clampUint32(value uint32, min, max uint32) uint32 {
	switch {
	case value < min:
		return min
	case value > max:
		return max
	default:
		return value
	}
}
