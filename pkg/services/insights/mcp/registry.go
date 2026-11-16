// Package mcp implements the in-room tool registry used by the AI agent.
//
// The package must not import pkg/models (models imports pkg/services/insights),
// so every backend a tool needs is expressed as a small interface that the
// wiring layer satisfies.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// ToolKind tells the executor where a tool runs.
type ToolKind string

const (
	// ToolKindServer executes synchronously inside b-server.
	ToolKindServer ToolKind = "server"
	// ToolKindClient is delegated to a participant's browser over NATS request/reply.
	ToolKindClient ToolKind = "client"
)

// Default per-tool timeouts, applied when Tool.Timeout is zero.
const (
	DefaultServerToolTimeout = 5 * time.Second
	// DefaultClientToolTimeout leaves room for first-time lazy loading of the
	// whiteboard plus the Yjs sync round trip before the client answers.
	DefaultClientToolTimeout = 12 * time.Second
)

// Tool is one callable tool: its model-facing declaration plus how to run it.
type Tool struct {
	Spec *insights.MCPToolSpec
	Kind ToolKind
	// Timeout bounds a single call; zero means the kind default.
	Timeout time.Duration
	// Essential tools stay callable while the boundary guard has the session
	// scope-locked. Only read-only tools and inert sentinels may set this.
	Essential bool
	// Handler runs server tools. Client tools leave it nil; the executor
	// routes them through ClientRPCSender instead.
	Handler func(ctx context.Context, call ToolCall) (ToolResult, error)
	// Prepare runs before a client tool is delegated, e.g. to open the shared
	// notepad. A returned error becomes the tool result.
	Prepare func(ctx context.Context, call ToolCall) error
}

// Name returns the tool name, or "" for an incomplete tool.
func (t *Tool) Name() string {
	if t == nil || t.Spec == nil {
		return ""
	}
	return t.Spec.Name
}

func (t *Tool) timeout() time.Duration {
	if t.Timeout > 0 {
		return t.Timeout
	}
	if t.Kind == ToolKindClient {
		return DefaultClientToolTimeout
	}
	return DefaultServerToolTimeout
}

// ToolCall is a single model-emitted tool invocation.
type ToolCall struct {
	CallID      string
	RoomID      string
	SessionID   string
	AgentUserID string
	ToolName    string
	ArgsJSON    string
	TraceID     string
}

// ToolResult is what the executor always returns, on success and on failure.
// OutputJSON is model-facing; error results carry {"ok":false,"error":"..."}.
type ToolResult struct {
	CallID     string
	ToolName   string
	OutputJSON string
	IsError    bool
	Duration   time.Duration
}

// Registry holds the tools available to one room session.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]*Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]*Tool)}
}

// Register adds a tool, replacing any previous tool with the same name.
// Incomplete tools (no spec or no name) are ignored.
func (r *Registry) Register(t *Tool) {
	name := t.Name()
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[name] = t
}

// Get looks a tool up by name.
func (r *Registry) Get(name string) (*Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns the model-facing declarations, ordered by name.
func (r *Registry) Specs() []*insights.MCPToolSpec {
	r.mu.RLock()
	specs := make([]*insights.MCPToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, t.Spec)
	}
	r.mu.RUnlock()

	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

// decodeArgs unmarshals a call's arguments into out, treating empty JSON as {}.
func decodeArgs(call ToolCall, out any) error {
	raw := call.ArgsJSON
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("arguments must be a JSON object")
	}
	return nil
}

// resultJSON builds a successful result from v.
func resultJSON(call ToolCall, v any) ToolResult {
	encoded, err := json.Marshal(v)
	if err != nil {
		return errorResult(call, "the result could not be encoded")
	}
	return ToolResult{CallID: call.CallID, ToolName: call.ToolName, OutputJSON: string(encoded)}
}

// errorResult builds a model-readable error result.
func errorResult(call ToolCall, format string, args ...any) ToolResult {
	encoded, err := json.Marshal(map[string]any{
		"ok":    false,
		"error": fmt.Sprintf(format, args...),
	})
	if err != nil {
		encoded = []byte(`{"ok":false,"error":"the tool failed"}`)
	}
	return ToolResult{
		CallID:     call.CallID,
		ToolName:   call.ToolName,
		OutputJSON: string(encoded),
		IsError:    true,
	}
}
