package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
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
	aiAgentMaxAllowedParticipantIds = 16

	// Shape limits of one external MCP server entry.
	agentMCPMaxKeyChars         = 64
	agentMCPMaxURLChars         = 1024
	agentMCPMaxAllowedTools     = 8
	agentMCPMaxAllowedToolChars = 128
)

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
	if m == nil {
		return nil
	}
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

// sendClientToolCall delivers the call to the participant's browser. A model
// without a NATS service (unit tests, a partially wired server) has no
// transport, so nothing is delivered.
func (m *AIAgentModel) sendClientToolCall(roomId, targetUserID string, msg *plugnmeet.AIAgentToolCallMsg) error {
	if m.natsService == nil {
		return nil
	}
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
		Notepad:      aiAgentNotepadBackend{m: m},
		Whiteboard:   aiAgentWhiteboardBackend{m: m},
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

	// the pre-authorized participants are host input, the rest is runtime state
	out.AllowedParticipantIds = cleanAllowedParticipantIds(out.GetAllowedParticipantIds())
	out.IsEnabled = false
	out.ActiveSessionId = nil

	return out
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

// ValidateAIAgentFeatures enforces the activation rules that must hold both when
// a room is created and when the agent is started manually.
func (m *AIAgentModel) ValidateAIAgentFeatures(f *plugnmeet.InsightsAIAgentFeatures, e2ee *plugnmeet.EndToEndEncryptionFeatures, insightsAllowed bool) error {
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
	if _, _, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent); err != nil {
		return config.ErrAIAgentProviderMissing
	}
	if err := ValidateExternalMCPServers(f.GetMcpServers()); err != nil {
		return err
	}

	return nil
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
	if m == nil {
		return
	}
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
		if m.logger != nil {
			m.logger.WithError(err).WithField("room_id", roomId).Warn("external mcp servers were not warmed")
		}
		servers = nil
	}
	if m.redisService == nil {
		return
	}
	if len(servers) == 0 {
		// A room recreated without external servers must not inherit the state
		// of an older session that used the same room id.
		_ = m.redisService.ClearExternalMCPTools(ctx, roomId)
		return
	}

	mcp.WarmRoomExternalMCP(ctx, m.redisService, roomId, servers, agentFeatures.GetMaxSessionSeconds(), m.logger)
}

// isOneOnOneAIAgentRoom reports whether the voice agent may run in this room: a
// breakout room is always a 1-on-1 session room, while a normal room only when
// it serves a single pre-authorized participant and no per-user sessions.
func isOneOnOneAIAgentRoom(meta *plugnmeet.RoomMetadata) bool {
	if meta == nil {
		return false
	}
	aiFeatures := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
	if !aiFeatures.GetIsAllow() {
		return false
	}
	if meta.GetIsBreakoutRoom() {
		return true
	}

	return !aiFeatures.GetAutoCreatePerUserSession() && len(aiFeatures.GetAllowedParticipantIds()) == 1
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
