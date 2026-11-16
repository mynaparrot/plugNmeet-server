package mcp

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ClientRPCSender delegates a tool call to a participant's browser (a-client).
type ClientRPCSender interface {
	CallClientTool(ctx context.Context, call ToolCall, timeout time.Duration) (ToolResult, error)
}

// ExecutorOptions configures an Executor.
type ExecutorOptions struct {
	Registry *Registry
	// Client executes ToolKindClient tools; nil disables client tools.
	Client ClientRPCSender
	// ScopeLocked reports whether the boundary guard has locked the session to
	// its topic. Optional.
	ScopeLocked func() bool
}

// Executor runs tool calls for one room session.
//
// Calls are serialized per session so concurrent whiteboard, poll and notepad
// operations can never interleave. Execute never returns an empty result: every
// failure becomes a model-readable ToolResult with IsError set.
type Executor struct {
	registry    *Registry
	client      ClientRPCSender
	scopeLocked func() bool

	mu       sync.Mutex
	sessions map[string]*sync.Mutex
}

// NewExecutor builds an executor for registry.
func NewExecutor(opts ExecutorOptions) *Executor {
	registry := opts.Registry
	if registry == nil {
		registry = NewRegistry()
	}
	return &Executor{
		registry:    registry,
		client:      opts.Client,
		scopeLocked: opts.ScopeLocked,
		sessions:    make(map[string]*sync.Mutex),
	}
}

// Execute runs one tool call and always returns a result.
func (e *Executor) Execute(ctx context.Context, call ToolCall) ToolResult {
	start := time.Now()

	result := e.execute(ctx, call)
	result.CallID = call.CallID
	result.ToolName = call.ToolName
	result.Duration = time.Since(start)
	if result.OutputJSON == "" {
		result.OutputJSON = "{}"
	}
	return result
}

func (e *Executor) execute(ctx context.Context, call ToolCall) ToolResult {
	tool, ok := e.registry.Get(call.ToolName)
	if !ok {
		return errorResult(call, "unknown tool %q. Use one of the tools you were given.", call.ToolName)
	}
	if e.scopeLocked != nil && e.scopeLocked() && !tool.Essential {
		return errorResult(call, "tool %q is disabled until the conversation returns to the session topic. Do not retry it; speak to the participant instead.", call.ToolName)
	}
	if err := validateArgs(tool.Spec, call.ArgsJSON); err != nil {
		return errorResult(call, "%s", err.Error())
	}

	// Serialize per session: whiteboard and poll operations must not race.
	unlock := e.lockSession(call.SessionID)
	defer unlock()

	timeout := tool.timeout()
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return e.run(toolCtx, tool, call, timeout)
}

func (e *Executor) run(ctx context.Context, tool *Tool, call ToolCall, timeout time.Duration) ToolResult {
	if tool.Kind == ToolKindClient {
		if e.client == nil {
			return errorResult(call, "the browser side of this tool is not available right now. Speak your instruction instead.")
		}
		if tool.Prepare != nil {
			// The room state the browser tool relies on must exist before the
			// call is dispatched, so the client never races the broadcast.
			if err := tool.Prepare(ctx, call); err != nil {
				return errorResult(call, "%s", err.Error())
			}
		}
		result, err := e.client.CallClientTool(ctx, call, timeout)
		return e.normalize(call, result, err, timeout)
	}

	if tool.Handler == nil {
		return errorResult(call, "tool %q is not implemented", tool.Spec.Name)
	}
	result, err := tool.Handler(ctx, call)
	return e.normalize(call, result, err, timeout)
}

func (e *Executor) normalize(call ToolCall, result ToolResult, err error, timeout time.Duration) ToolResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errorResult(call, "%q did not answer within %s. Do not retry more than once.", call.ToolName, timeout)
	case errors.Is(err, context.Canceled):
		return errorResult(call, "%q was cancelled because the session is ending.", call.ToolName)
	case err != nil:
		return errorResult(call, "%s", err.Error())
	case result.OutputJSON == "":
		return errorResult(call, "%q returned no result. Do not retry it.", call.ToolName)
	}
	return result
}

// lockSession returns the unlock function for a session's mutex.
func (e *Executor) lockSession(sessionID string) func() {
	if sessionID == "" {
		sessionID = "_default"
	}
	e.mu.Lock()
	lock, ok := e.sessions[sessionID]
	if !ok {
		lock = &sync.Mutex{}
		e.sessions[sessionID] = lock
	}
	e.mu.Unlock()

	lock.Lock()
	return lock.Unlock
}
