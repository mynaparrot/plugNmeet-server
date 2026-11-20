package mcp

// External MCP servers: a room host may point the AI agent at up to two remote
// MCP servers. Everything here is written for an untrusted peer: the URL is
// validated before every connection, only read-only tools are exposed, schemas
// and outputs are bounded, responses are framed as untrusted documents and the
// whole process shares one small concurrency budget.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// Hard limits. They are deliberately small: an external server is untrusted and
// must never be able to flood the model, the registry or the cache.
const (
	MaxExternalMCPServersPerRoom          = 2
	MaxToolsPerExternalMCPServer          = 5
	MaxTotalExternalMCPTools              = 8
	MaxExternalMCPDescriptionChars        = 240
	maxExternalMCPURLChars                = 1024
	maxExternalMCPAllowedToolChars        = 128
	MaxExternalMCPSchemaBytes             = 2048
	MaxExternalMCPSchemaProps             = 8
	MaxExternalMCPHTTPBodyBytes           = 256 << 10
	MaxExternalMCPOutputChars             = 4000
	MaxExternalMCPCacheEntryBytes         = 8 << 10
	MaxExternalMCPCacheEntriesPerRoom     = 20
	MaxUncachedExternalMCPCallsPerSession = 30
	ExternalMCPRequestTimeout             = 5 * time.Second
	ExternalMCPGlobalConcurrency          = 16
)

// Consumer scopes of an external MCP server. A server with the shared scope is
// usable by every consumer; any other scope is only registered for the
// consumer that owns it. Empty is treated as shared, so older room configs
// keep working.
const (
	MCPScopeShared   = "shared"
	MCPScopeTextChat = "text_chat"
	MCPScopeAIAgent  = "ai_agent"
)

const (
	externalMCPUserAgent     = "plugNmeet-AIAgent/1.0"
	externalMCPClientName    = "plugnmeet-ai-agent"
	externalMCPClientVersion = "1.0.0"

	externalMCPResourceToolName = "read_resource"
	externalMCPDescriptionChars = 160

	externalMCPKeyTTL    = 2 * time.Hour
	externalMCPAquireTTL = time.Second

	externalMCPClientTimeout = ExternalMCPRequestTimeout

	// externalMCPRegistrationTimeout bounds the Redis read done while a session
	// registry is built.
	externalMCPRegistrationTimeout = 2 * time.Second
)

var (
	errExternalMCPResponseTooLarge = errors.New("the mcp server response was too large")
	errExternalMCPBusy             = errors.New("the mcp client is busy")
	errExternalMCPSchemaTooLarge   = errors.New("the tool schema is too large")
)

// externalMCPConcurrency is the process-wide budget for outbound round-trips.
var externalMCPConcurrency = make(chan struct{}, ExternalMCPGlobalConcurrency)

// forbiddenRequestHeaders can never be set by a room's server configuration.
var forbiddenRequestHeaders = map[string]struct{}{
	"host":              {},
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
	"upgrade":           {},
	"keep-alive":        {},
	"proxy-connection":  {},
	"te":                {},
	"trailer":           {},
}

// SanitizeExternalMCPHeaders trims a configured header set and drops the names
// the transport owns, so a room can never rewrite its own transport hop.
func SanitizeExternalMCPHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	clean := make(map[string]string, len(headers))
	for name, value := range headers {
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		if _, forbidden := forbiddenRequestHeaders[strings.ToLower(name)]; forbidden {
			continue
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		clean[name] = value
	}
	if len(clean) == 0 {
		return nil
	}
	return clean
}

// CachedExternalMCPTool is one pre-warmed tool description; Redis stores it and
// the registry consumes it.
type CachedExternalMCPTool = insights.CachedExternalMCPTool

// NormalizeMCPScope maps a host-provided scope onto one of the known scopes.
// Anything unrecognised (including empty) becomes the shared scope, which keeps
// the historical behaviour of exposing an unlabelled server to every consumer.
func NormalizeMCPScope(raw string) string {
	scope := strings.TrimSpace(strings.ToLower(raw))
	scope = strings.NewReplacer("-", "_", " ", "_").Replace(scope)
	switch scope {
	case MCPScopeTextChat:
		return MCPScopeTextChat
	case MCPScopeAIAgent:
		return MCPScopeAIAgent
	}
	return MCPScopeShared
}

// mcpScopeAllows reports whether a consumer may use a server. An unscoped
// consumer accepts everything; a shared server is accepted by everyone.
func mcpScopeAllows(serverScope, consumerScope string) bool {
	if consumerScope == "" {
		return true
	}
	if NormalizeMCPScope(serverScope) == MCPScopeShared {
		return true
	}
	return NormalizeMCPScope(serverScope) == NormalizeMCPScope(consumerScope)
}

// scopedMCPServerConfig copies a host-provided server and stamps scope when the
// host did not set one, so the stored config always says who owns it.
func scopedMCPServerConfig(cfg *plugnmeet.ExternalMCPServerConfig, scope string) *plugnmeet.ExternalMCPServerConfig {
	if cfg == nil {
		return nil
	}
	serverScope := strings.TrimSpace(cfg.GetScope())
	if serverScope == "" {
		serverScope = scope
	}

	tools := make([]string, 0, len(cfg.GetAllowedTools()))
	for _, tool := range cfg.GetAllowedTools() {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if len(tools) >= MaxToolsPerExternalMCPServer {
			break
		}
		tools = append(tools, truncateRunes(tool, maxExternalMCPAllowedToolChars))
	}
	if len(tools) == 0 {
		tools = nil
	}

	return &plugnmeet.ExternalMCPServerConfig{
		Url:          truncateRunes(strings.TrimSpace(cfg.GetUrl()), maxExternalMCPURLChars),
		Headers:      SanitizeExternalMCPHeaders(cfg.GetHeaders()),
		AllowedTools: tools,
		Scope:        &serverScope,
	}
}

// externalMCPServerTarget is everything needed to open a session.
type externalMCPServerTarget struct {
	ServerKey  string
	ServerSlug string
	Endpoint   string
	Headers    map[string]string
}

// externalMCPToolTarget adds the remote tool a registered tool maps to.
type externalMCPToolTarget struct {
	externalMCPServerTarget
	RemoteToolName string
	IsResourceRead bool
}

// externalMCPTTL derives the TTL for every external MCP key: long enough for
// the whole session, never longer than externalMCPKeyTTL. Without a known
// session length it returns 0, which leaves the TTL to RedisService's default
// instead of pinning a guess.
func externalMCPTTL(maxSessionSeconds uint32) time.Duration {
	if maxSessionSeconds == 0 {
		return 0
	}

	return min(time.Duration(maxSessionSeconds)*time.Second+time.Hour, externalMCPKeyTTL)
}

// ExternalMCPManager discovers, registers and calls external MCP tools.
type ExternalMCPManager struct {
	store             *redisservice.RedisService
	logger            *logrus.Entry
	allowInsecureHTTP bool
	client            *http.Client
	ttl               time.Duration
	// scope is the consumer scope of the session the registry serves; only the
	// matching and shared servers are registered.
	scope string
}

// NewExternalMCPManager wires a manager from the room's dependencies.
func NewExternalMCPManager(store *redisservice.RedisService, deps ToolDependencies) *ExternalMCPManager {
	if store == nil {
		return nil
	}
	return &ExternalMCPManager{
		store:             store,
		logger:            deps.logger(),
		allowInsecureHTTP: deps.AllowInsecureHTTP,
		client:            newExternalMCPHTTPClient(deps.HTTPClient, deps.AllowInsecureHTTP),
		ttl:               0,
		scope:             deps.MCPScope,
	}
}

// WarmRoomExternalMCP discovers the room's external servers and caches their
// read-only tools. Failures are logged, never fatal.
func WarmRoomExternalMCP(ctx context.Context, store *redisservice.RedisService, roomID string, servers map[string]*plugnmeet.ExternalMCPServerConfig, maxSessionSeconds uint32, logger *logrus.Entry) []*CachedExternalMCPTool {
	manager := NewExternalMCPManager(store, ToolDependencies{RedisService: store, RoomID: roomID, Logger: logger})
	if manager == nil {
		return nil
	}
	// The whole warm-up shares one budget: joining the room must not wait on a
	// slow remote server.
	warmCtx, cancel := context.WithTimeout(ctx, ExternalMCPRequestTimeout)
	defer cancel()
	return manager.Warm(warmCtx, roomID, servers, maxSessionSeconds)
}

// EnsureRoomExternalMCP reuses the tools warmed at room creation and only
// discovers anything when nothing is cached yet, so a session start normally
// costs zero network calls. When the room's metadata no longer carries the
// servers (they are stripped before the metadata is broadcast) the stored
// configuration is used instead.
func EnsureRoomExternalMCP(ctx context.Context, store *redisservice.RedisService, roomID string, servers map[string]*plugnmeet.ExternalMCPServerConfig, maxSessionSeconds uint32, logger *logrus.Entry) []*CachedExternalMCPTool {
	if store == nil || roomID == "" {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, externalMCPRegistrationTimeout)
	defer cancel()

	if cached, err := store.LoadExternalMCPTools(readCtx, roomID); err == nil && len(cached) > 0 {
		return cached
	}
	if len(servers) == 0 {
		stored, err := store.LoadExternalMCPConfig(readCtx, roomID)
		if err != nil || len(stored) == 0 {
			return nil
		}
		servers = stored
	}
	return WarmRoomExternalMCP(ctx, store, roomID, servers, maxSessionSeconds, logger)
}

// UpdateRoomScopedExternalMCP replaces the servers one consumer owns without
// disturbing the other consumers' servers, then re-warms the merged set. It is
// the update path of the live room settings, where the text chat and the AI
// agent each manage their own servers within the room-wide budget.
func UpdateRoomScopedExternalMCP(ctx context.Context, store *redisservice.RedisService, roomID string, scope string, servers map[string]*plugnmeet.ExternalMCPServerConfig, maxSessionSeconds uint32, logger *logrus.Entry) []*CachedExternalMCPTool {
	if store == nil || roomID == "" {
		return nil
	}
	log := logger
	if log == nil {
		log = logrus.NewEntry(logrus.StandardLogger())
	}

	normalized := NormalizeMCPScope(scope)
	existing, err := store.LoadExternalMCPConfig(ctx, roomID)
	if err != nil {
		log.WithField("room_id", roomID).WithError(err).Warn("external mcp config could not be loaded")
		existing = nil
	}

	merged := make(map[string]*plugnmeet.ExternalMCPServerConfig, MaxExternalMCPServersPerRoom)
	// The servers of the other consumers survive the update. The updating
	// consumer's own entries are replaced; a text chat update also replaces the
	// shared entries, because they used to be its only way to configure servers.
	for _, key := range sortedServerKeys(existing) {
		cfg := existing[key]
		if cfg == nil || strings.TrimSpace(cfg.GetUrl()) == "" {
			continue
		}
		serverScope := NormalizeMCPScope(cfg.GetScope())
		if serverScope == normalized {
			continue
		}
		if normalized == MCPScopeTextChat && serverScope == MCPScopeShared {
			continue
		}
		if len(merged) >= MaxExternalMCPServersPerRoom {
			continue
		}
		merged[key] = cfg
	}

	for _, key := range sortedServerKeys(servers) {
		cfg := servers[key]
		if cfg == nil || strings.TrimSpace(cfg.GetUrl()) == "" {
			continue
		}
		if len(merged) >= MaxExternalMCPServersPerRoom {
			log.WithFields(logrus.Fields{"room_id": roomID, "server": key, "scope": normalized}).
				Warn("external mcp server skipped: the room's server limit is reached")
			continue
		}
		merged[key] = scopedMCPServerConfig(cfg, normalized)
	}

	if len(merged) == 0 {
		// No consumer has servers left: drop the state of the old configuration.
		_ = store.ClearExternalMCPTools(ctx, roomID)
		return nil
	}
	return WarmRoomExternalMCP(ctx, store, roomID, merged, maxSessionSeconds, log)
}

// Warm performs discovery and persists the config plus the pre-warmed tools.
func (m *ExternalMCPManager) Warm(ctx context.Context, roomID string, servers map[string]*plugnmeet.ExternalMCPServerConfig, maxSessionSeconds uint32) []*CachedExternalMCPTool {
	if m == nil || m.store == nil || roomID == "" || len(servers) == 0 {
		return nil
	}
	m.ttl = externalMCPTTL(maxSessionSeconds)

	// The configuration is stored before any network call: a transient failure
	// during room creation must not lose it, because the metadata that carried
	// the servers is stripped and the session start retries from this copy.
	configured := map[string]*plugnmeet.ExternalMCPServerConfig{}
	for _, key := range sortedServerKeys(servers) {
		if len(configured) >= MaxExternalMCPServersPerRoom {
			break
		}
		cfg := servers[key]
		if cfg == nil || strings.TrimSpace(cfg.GetUrl()) == "" {
			continue
		}
		configured[key] = cfg
	}
	if len(configured) == 0 {
		// Nothing usable was configured: drop any state of a previous
		// configuration.
		_ = m.store.ClearExternalMCPTools(ctx, roomID)
		return nil
	}
	if err := m.store.SaveExternalMCPConfig(ctx, roomID, configured, m.ttl); err != nil {
		m.logger.WithField("room_id", roomID).WithError(err).Warn("external mcp config could not be cached")
		return nil
	}

	var tools []*CachedExternalMCPTool
	usedSlugs := map[string]int{}
	for _, key := range sortedServerKeys(configured) {
		if len(tools) >= MaxTotalExternalMCPTools {
			break
		}
		cfg := configured[key]
		slug := uniqueExternalMCPSlug(externalMCPSlug(key), usedSlugs)
		target := externalMCPServerTarget{
			ServerKey:  key,
			ServerSlug: slug,
			Endpoint:   strings.TrimSpace(cfg.GetUrl()),
			Headers:    cfg.GetHeaders(),
		}
		discovered, err := m.discover(ctx, target, cfg.GetAllowedTools())
		if err != nil {
			m.logger.WithFields(map[string]any{"room_id": roomID, "server": key}).
				WithError(err).Warn("external mcp server discovery failed")
			continue
		}
		if len(discovered) == 0 {
			continue
		}
		if remaining := MaxTotalExternalMCPTools - len(tools); len(discovered) > remaining {
			discovered = discovered[:remaining]
		}
		tools = append(tools, discovered...)
	}

	if len(tools) == 0 {
		// The servers answered with nothing usable: drop the tools cached for a
		// previous configuration but keep the config for the retry at start.
		_ = m.store.ClearExternalMCPCachedTools(ctx, roomID)
		return nil
	}
	if err := m.store.SaveExternalMCPTools(ctx, roomID, tools, m.ttl); err != nil {
		m.logger.WithField("room_id", roomID).WithError(err).Warn("external mcp tools could not be cached")
		return nil
	}
	m.logger.WithFields(map[string]any{"room_id": roomID, "servers": len(configured), "tools": len(tools)}).
		Info("external mcp tools warmed")
	return tools
}

// registerExternalMCPTools attaches the room's cached external tools. It reads
// Redis only, so a slow remote server never delays a session.
func registerExternalMCPTools(reg *Registry, deps ToolDependencies) {
	if deps.RedisService == nil || deps.RoomID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalMCPRegistrationTimeout)
	defer cancel()
	NewExternalMCPManager(deps.RedisService, deps).RegisterCached(ctx, reg, deps.RoomID)
}

// RegisterCached adds the room's pre-warmed external tools to reg.
func (m *ExternalMCPManager) RegisterCached(ctx context.Context, reg *Registry, roomID string) {
	if m == nil || m.store == nil || reg == nil || roomID == "" {
		return
	}
	servers, err := m.store.LoadExternalMCPConfig(ctx, roomID)
	if err != nil || len(servers) == 0 {
		if err != nil {
			m.logger.WithField("room_id", roomID).WithError(err).Warn("external mcp config could not be loaded")
		}
		return
	}
	cached, err := m.store.LoadExternalMCPTools(ctx, roomID)
	if err != nil || len(cached) == 0 {
		if err != nil {
			m.logger.WithField("room_id", roomID).WithError(err).Warn("external mcp tools could not be loaded")
		}
		return
	}

	uncached := &atomic.Int32{}
	registered := 0
	for _, entry := range cached {
		if registered >= MaxTotalExternalMCPTools {
			break
		}
		if entry == nil || entry.Spec == nil || entry.Spec.Name == "" {
			continue
		}
		server := servers[entry.ServerKey]
		if server == nil || strings.TrimSpace(server.GetUrl()) == "" {
			continue
		}
		// A server belongs to the feature that configured it: the AI agent must
		// not call the text chat's servers and vice versa.
		if !mcpScopeAllows(server.GetScope(), m.scope) {
			continue
		}
		if _, exists := reg.Get(entry.Spec.Name); exists {
			continue
		}
		target := externalMCPToolTarget{
			externalMCPServerTarget: externalMCPServerTarget{
				ServerKey:  entry.ServerKey,
				ServerSlug: entry.ServerSlug,
				Endpoint:   strings.TrimSpace(server.GetUrl()),
				Headers:    server.GetHeaders(),
			},
			RemoteToolName: entry.RemoteToolName,
			IsResourceRead: entry.IsResourceRead,
		}
		reg.Register(&Tool{
			Kind:    ToolKindServer,
			Spec:    entry.Spec,
			Timeout: ExternalMCPRequestTimeout + 2*time.Second,
			Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
				return m.runExternalTool(ctx, call, target, uncached)
			},
		})
		registered++
	}
}

// runExternalTool serves one call, from Redis when possible.
func (m *ExternalMCPManager) runExternalTool(ctx context.Context, call ToolCall, target externalMCPToolTarget, uncached *atomic.Int32) (ToolResult, error) {
	field := externalMCPCacheField(target.ServerKey, target.RemoteToolName, call.ArgsJSON)
	if cached, ok := m.store.LoadExternalMCPCachedOutput(ctx, call.RoomID, field); ok {
		return ToolResult{CallID: call.CallID, ToolName: call.ToolName, OutputJSON: cached}, nil
	}

	if uncached != nil {
		if uncached.Load() >= MaxUncachedExternalMCPCallsPerSession {
			return errorResult(call, "This session has used its %d live external lookups. Answer from what you already have; repeated questions still come from the cache.", MaxUncachedExternalMCPCallsPerSession), nil
		}
		uncached.Add(1)
	}

	result, err := m.callExternalMCP(ctx, call, target)
	if err != nil || result.IsError {
		return result, err
	}
	m.store.SaveExternalMCPCachedOutput(ctx, call.RoomID, field, result.OutputJSON, MaxExternalMCPCacheEntryBytes, MaxExternalMCPCacheEntriesPerRoom, m.ttl)
	return result, nil
}

// callExternalMCP opens a short-lived session, runs the call and frames the
// plain-text answer as an untrusted document.
func (m *ExternalMCPManager) callExternalMCP(ctx context.Context, call ToolCall, target externalMCPToolTarget) (ToolResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, ExternalMCPRequestTimeout)
	defer cancel()

	session, err := m.openSession(callCtx, target.externalMCPServerTarget)
	if err != nil {
		m.logger.WithFields(map[string]any{"room_id": call.RoomID, "server": target.ServerKey}).
			WithError(err).Warn("external mcp session could not be opened")
		return errorResult(call, "That external tool could not be reached right now."), nil
	}
	defer session.Close()

	var text string
	if target.IsResourceRead {
		var in struct {
			URI string `json:"uri"`
		}
		if err := decodeArgs(call, &in); err != nil {
			return errorResult(call, "%s", err.Error()), nil
		}
		if strings.TrimSpace(in.URI) == "" {
			return errorResult(call, "uri is required"), nil
		}
		result, err := session.ReadResource(callCtx, &mcpsdk.ReadResourceParams{URI: strings.TrimSpace(in.URI)})
		if err != nil {
			m.logger.WithFields(map[string]any{"room_id": call.RoomID, "server": target.ServerKey}).
				WithError(err).Warn("external mcp resource read failed")
			return errorResult(call, "That external document could not be read right now."), nil
		}
		text = externalMCPResourceText(result)
	} else {
		args, err := externalMCPArguments(call.ArgsJSON)
		if err != nil {
			return errorResult(call, "%s", err.Error()), nil
		}
		result, err := session.CallTool(callCtx, &mcpsdk.CallToolParams{Name: target.RemoteToolName, Arguments: args})
		if err != nil {
			m.logger.WithFields(map[string]any{"room_id": call.RoomID, "server": target.ServerKey, "tool": target.RemoteToolName}).
				WithError(err).Warn("external mcp tool call failed")
			return errorResult(call, "That external tool failed right now."), nil
		}
		if result.IsError {
			return errorResult(call, "That external tool reported it could not answer."), nil
		}
		text = externalMCPContentText(result.Content)
	}

	body, truncated := sanitizeExternalMCPText(text)
	if body == "" {
		body = "(the external tool returned no text)"
	}
	return resultJSON(call, map[string]any{
		"server":    target.ServerKey,
		"tool":      target.RemoteToolName,
		"untrusted": true,
		"truncated": truncated,
		"content":   frameExternalMCPOutput(target.ServerKey, target.RemoteToolName, body),
	}), nil
}

// discover lists one page of tools (plus one page of resources) for a server.
func (m *ExternalMCPManager) discover(ctx context.Context, target externalMCPServerTarget, allowed []string) ([]*CachedExternalMCPTool, error) {
	if _, err := ValidateExternalMCPURL(ctx, target.Endpoint, m.allowInsecureHTTP); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, ExternalMCPRequestTimeout)
	defer cancel()

	session, err := m.openSession(callCtx, target)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	// One page only: a hostile server must not be able to make us paginate.
	listing, err := session.ListTools(callCtx, &mcpsdk.ListToolsParams{})
	if err != nil {
		return nil, err
	}

	tools := make([]*CachedExternalMCPTool, 0, MaxToolsPerExternalMCPServer)
	for _, tool := range listing.Tools {
		if len(tools) >= MaxToolsPerExternalMCPServer {
			break
		}
		if !isAllowedReadOnlyMCPTool(tool, allowed) {
			continue
		}
		schema, err := sanitizeExternalMCPInputSchema(tool.InputSchema)
		if err != nil {
			continue
		}
		tools = append(tools, &CachedExternalMCPTool{
			ServerKey:      target.ServerKey,
			ServerSlug:     target.ServerSlug,
			RemoteToolName: tool.Name,
			Spec: &insights.MCPToolSpec{
				Name:        externalMCPToolName(target.ServerSlug, tool.Name),
				Description: externalMCPDescription(target.ServerKey, tool.Name, tool.Description),
				InputSchema: schema,
			},
		})
	}

	if len(tools) >= MaxToolsPerExternalMCPServer || !externalMCPResourceAllowed(allowed) {
		return tools, nil
	}
	resources, err := session.ListResources(callCtx, &mcpsdk.ListResourcesParams{})
	if err != nil || len(resources.Resources) == 0 {
		return tools, nil
	}
	tools = append(tools, &CachedExternalMCPTool{
		ServerKey:      target.ServerKey,
		ServerSlug:     target.ServerSlug,
		RemoteToolName: externalMCPResourceToolName,
		IsResourceRead: true,
		Spec: &insights.MCPToolSpec{
			Name: externalMCPToolName(target.ServerSlug, externalMCPResourceToolName),
			Description: truncateRunes(fmt.Sprintf("Read one document by uri from the %s documentation server and return it as plain text. "+
				"Use it before answering factual questions instead of guessing.", target.ServerKey), MaxExternalMCPDescriptionChars),
			InputSchema: objectSchema(map[string]any{
				"uri": stringProp("The uri of one document the server lists.", 1000),
			}, "uri"),
		},
	})
	return tools, nil
}

// openSession connects with zero client capabilities: no sampling, no
// elicitation, no roots.
func (m *ExternalMCPManager) openSession(ctx context.Context, target externalMCPServerTarget) (*mcpsdk.ClientSession, error) {
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    externalMCPClientName,
		Version: externalMCPClientVersion,
	}, nil)
	httpClient := m.clientFor(target.Headers)

	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             target.Endpoint,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err == nil {
		return session, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	// Older servers only speak the deprecated HTTP+SSE transport.
	session, sseErr := client.Connect(ctx, &mcpsdk.SSEClientTransport{
		Endpoint:   target.Endpoint,
		HTTPClient: httpClient,
	}, nil)
	if sseErr != nil {
		return nil, err
	}
	return session, nil
}

// clientFor returns a per-call client that injects the server's headers.
func (m *ExternalMCPManager) clientFor(headers map[string]string) *http.Client {
	client := *m.client
	if transport, ok := client.Transport.(*externalMCPTransport); ok {
		tuned := *transport
		tuned.headers = headers
		client.Transport = &tuned
	}
	return &client
}

// newExternalMCPHTTPClient hardens the HTTP client used for MCP round-trips.
func newExternalMCPHTTPClient(baseClient *http.Client, allowInsecureHTTP bool) *http.Client {
	if baseClient == nil {
		dialer := &net.Dialer{
			Timeout: ExternalMCPRequestTimeout,
			Control: externalMCPDialControl(allowInsecureHTTP),
		}
		baseClient = &http.Client{
			Timeout: externalMCPClientTimeout,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				TLSHandshakeTimeout:   ExternalMCPRequestTimeout,
				ResponseHeaderTimeout: ExternalMCPRequestTimeout,
				DisableCompression:    true,
				DisableKeepAlives:     true,
				MaxIdleConns:          1,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	client := *baseClient
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = &externalMCPTransport{base: base}
	return &client
}

// externalMCPDialControl refuses anything but a global address on port 443.
func externalMCPDialControl(allowInsecureHTTP bool) func(network, address string, conn syscall.RawConn) error {
	if allowInsecureHTTP {
		return nil
	}
	return func(_, address string, _ syscall.RawConn) error {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return errExternalMCPURLBlocked
		}
		if port != "443" {
			return errExternalMCPPortBlocked
		}
		if blockedIP(net.ParseIP(host)) {
			return errExternalMCPURLBlocked
		}
		return nil
	}
}

// externalMCPTransport injects the configured headers, enforces the global
// concurrency budget and caps every response body.
type externalMCPTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *externalMCPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	select {
	case externalMCPConcurrency <- struct{}{}:
		defer func() { <-externalMCPConcurrency }()
	case <-time.After(externalMCPAquireTTL):
		return nil, errExternalMCPBusy
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}

	outbound := req.Clone(req.Context())
	for name, value := range t.headers {
		if _, forbidden := forbiddenRequestHeaders[strings.ToLower(strings.TrimSpace(name))]; forbidden {
			continue
		}
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		outbound.Header.Set(name, value)
	}
	outbound.Header.Set("User-Agent", externalMCPUserAgent)

	resp, err := t.base.RoundTrip(outbound)
	if err != nil {
		return nil, err
	}
	if resp.ContentLength > MaxExternalMCPHTTPBodyBytes {
		_ = resp.Body.Close()
		return nil, errExternalMCPResponseTooLarge
	}
	resp.Body = &limitedReadCloser{body: resp.Body}
	return resp, nil
}

// limitedReadCloser stops reading the moment the body exceeds the hard limit,
// so a hostile server cannot make b-server buffer a huge payload.
type limitedReadCloser struct {
	body io.ReadCloser
	read int64
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	if l.read > MaxExternalMCPHTTPBodyBytes {
		return 0, errExternalMCPResponseTooLarge
	}
	if remaining := int64(MaxExternalMCPHTTPBodyBytes) + 1 - l.read; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := l.body.Read(p)
	l.read += int64(n)
	if l.read > MaxExternalMCPHTTPBodyBytes {
		_ = l.body.Close()
		return 0, errExternalMCPResponseTooLarge
	}
	return n, err
}

func (l *limitedReadCloser) Close() error {
	return l.body.Close()
}

// externalMCPCacheField is the Redis hash field for one canonical call.
func externalMCPCacheField(serverKey, remoteToolName, argsJSON string) string {
	canonical := canonicalExternalMCPArgs(argsJSON)
	sum := sha256.Sum256([]byte(serverKey + "\x00" + remoteToolName + "\x00" + canonical))
	return hex.EncodeToString(sum[:])
}

func canonicalExternalMCPArgs(argsJSON string) string {
	raw := strings.TrimSpace(argsJSON)
	if raw == "" {
		return "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return string(encoded)
}

// externalMCPArguments decodes the model's arguments for the remote call.
func externalMCPArguments(argsJSON string) (map[string]any, error) {
	raw := strings.TrimSpace(argsJSON)
	if raw == "" {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// externalMCPContentText keeps plain text only: images, audio and binary blobs
// are dropped.
func externalMCPContentText(content []mcpsdk.Content) string {
	var b strings.Builder
	for _, item := range content {
		switch typed := item.(type) {
		case *mcpsdk.TextContent:
			appendExternalMCPText(&b, typed.Text)
		case *mcpsdk.EmbeddedResource:
			if typed.Resource != nil && len(typed.Resource.Blob) == 0 {
				appendExternalMCPText(&b, typed.Resource.Text)
			}
		}
	}
	return b.String()
}

func externalMCPResourceText(result *mcpsdk.ReadResourceResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, item := range result.Contents {
		if item == nil || len(item.Blob) > 0 {
			continue
		}
		appendExternalMCPText(&b, item.Text)
	}
	return b.String()
}

func appendExternalMCPText(b *strings.Builder, text string) {
	if text == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	b.WriteString(text)
}

var reExternalMCPFrameBreaker = regexp.MustCompile(`(?i)\[\s*/?\s*UNTRUSTED EXTERNAL DOCUMENT[^\]]*\]`)

// sanitizeExternalMCPText strips control characters, defuses frame breakers and
// truncates to the model-facing budget.
func sanitizeExternalMCPText(text string) (string, bool) {
	text = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, text)
	text = reExternalMCPFrameBreaker.ReplaceAllString(text, "[removed]")
	text = strings.TrimSpace(text)

	truncated := false
	if utf8.RuneCountInString(text) > MaxExternalMCPOutputChars {
		text = truncateRunes(text, MaxExternalMCPOutputChars)
		truncated = true
	}
	return text, truncated
}

// frameExternalMCPOutput wraps text so the model can never mistake it for host
// instructions.
func frameExternalMCPOutput(serverKey, remoteTool, text string) string {
	var b strings.Builder
	b.WriteString(untrustedDocumentHeader)
	b.WriteString("\nsource: mcp:")
	b.WriteString(serverKey)
	b.WriteString("/")
	b.WriteString(remoteTool)
	b.WriteString("\n---\n")
	b.WriteString(text)
	b.WriteString("\n")
	b.WriteString(untrustedDocumentFooter)
	return b.String()
}

// externalMCPDescription bounds and neutralises the remote description.
func externalMCPDescription(serverKey, remoteName, description string) string {
	cleaned, _ := sanitizeExternalMCPText(description)
	prefix := fmt.Sprintf("(external %s mcp server) ", serverKey)
	if cleaned == "" {
		cleaned = fmt.Sprintf("Run %s on the %s documentation server.", remoteName, serverKey)
	}
	return truncateRunes(prefix+cleaned, MaxExternalMCPDescriptionChars)
}

// externalMCPResourceAllowed reports whether the host opted into resource reads.
func externalMCPResourceAllowed(allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	return containsString(allowed, externalMCPResourceToolName)
}

// externalMCPToolName namespaces a remote tool: ext_<server>_<tool>, at most 64
// characters and [a-z0-9_] only.
func externalMCPToolName(serverSlug, remoteName string) string {
	const maxName = 64
	server := externalMCPSlug(serverSlug)
	tool := externalMCPSlug(remoteName)
	name := "ext_" + server + "_" + tool
	if len(name) <= maxName {
		return name
	}
	budget := maxName - len("ext_") - len(server) - 1
	if budget < 1 {
		server = server[:max(maxName-len("ext_")-len(tool)-1, 1)]
		return "ext_" + server + "_" + tool
	}
	return "ext_" + server + "_" + tool[:budget]
}

// externalMCPSlug lowercases and sanitises a name for the model-facing tool name.
func externalMCPSlug(raw string) string {
	var b strings.Builder
	previousUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			previousUnderscore = false
		default:
			if !previousUnderscore && b.Len() > 0 {
				b.WriteRune('_')
				previousUnderscore = true
			}
		}
	}
	slug := strings.Trim(b.String(), "_")
	if slug == "" {
		slug = "server"
	}
	return slug
}

// uniqueExternalMCPSlug keeps two servers with similar names apart.
func uniqueExternalMCPSlug(slug string, used map[string]int) string {
	if used == nil {
		return slug
	}
	used[slug]++
	if count := used[slug]; count > 1 {
		return fmt.Sprintf("%s%d", slug, count)
	}
	return slug
}

func sortedServerKeys(servers map[string]*plugnmeet.ExternalMCPServerConfig) []string {
	keys := make([]string, 0, len(servers))
	for key := range servers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
