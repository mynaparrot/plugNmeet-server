package models

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
)

// waitForPendingClientCall waits until CallClientToolForUser registered its call
// and returns the generated call id.
func waitForPendingClientCall(t *testing.T, model *AIAgentModel) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		model.pendingClientMu.Lock()
		for callID := range model.pendingClientCalls {
			model.pendingClientMu.Unlock()
			return callID
		}
		model.pendingClientMu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the client tool call was not registered")

	return ""
}

func pendingClientCallCount(model *AIAgentModel) int {
	model.pendingClientMu.Lock()
	defer model.pendingClientMu.Unlock()

	return len(model.pendingClientCalls)
}

type clientCallOutcome struct {
	res mcp.ToolResult
	err error
}

func TestCallClientToolForUserResolvesOnlyForTargetedUser(t *testing.T) {
	model := newAIAgentTestModelWithNats(newAIAgentTestRedis(t))
	ctx := context.Background()

	done := make(chan clientCallOutcome, 1)
	go func() {
		res, err := model.CallClientToolForUser(ctx, "admin-1", mcp.ToolCall{
			RoomID:   "room-1",
			ToolName: mcp.ToolNotepadRead,
		}, 5*time.Second)
		done <- clientCallOutcome{res: res, err: err}
	}()

	callID := waitForPendingClientCall(t, model)
	if callID == "" {
		t.Fatal("the call id must be generated when the caller left it empty")
	}

	// A result from another participant or another room must never resolve it.
	model.HandleClientToolResult(ctx, "room-1", "someone-else", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"from":"wrong-user"}`,
	})
	model.HandleClientToolResult(ctx, "room-2", "admin-1", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"from":"wrong-room"}`,
	})
	if pendingClientCallCount(model) != 1 {
		t.Fatal("a result from the wrong user or room must not resolve the call")
	}

	model.HandleClientToolResult(ctx, "room-1", "admin-1", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"blocks":[]}`,
	})

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("the targeted call must resolve, got error %v", out.err)
		}
		if out.res.OutputJSON != `{"blocks":[]}` {
			t.Errorf("output = %q, want the browser's result", out.res.OutputJSON)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the targeted call was not resolved")
	}
	if pendingClientCallCount(model) != 0 {
		t.Error("a resolved call must be removed from the pending calls")
	}
}

func TestCallClientToolForUserReportsBrowserFailure(t *testing.T) {
	model := newAIAgentTestModelWithNats(newAIAgentTestRedis(t))
	ctx := context.Background()

	done := make(chan clientCallOutcome, 1)
	go func() {
		res, err := model.CallClientToolForUser(ctx, "admin-1", mcp.ToolCall{
			CallID:   "call-1",
			RoomID:   "room-1",
			ToolName: mcp.ToolWhiteboardCaptureSnapshot,
		}, 5*time.Second)
		done <- clientCallOutcome{res: res, err: err}
	}()

	waitForPendingClientCall(t, model)
	model.HandleClientToolResult(ctx, "room-1", "admin-1", &plugnmeet.AIAgentToolResultMsg{
		CallId: "call-1", Ok: false, Error: "the whiteboard is not ready",
	})

	select {
	case out := <-done:
		if out.err == nil || !strings.Contains(out.err.Error(), "the whiteboard is not ready") {
			t.Fatalf("a failed browser call must return its reason, got %v", out.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the failed call was not resolved")
	}
}

func TestCallClientToolForUserRejectsBlankTarget(t *testing.T) {
	model := newAIAgentTestModelWithNats(newAIAgentTestRedis(t))

	if _, err := model.CallClientToolForUser(context.Background(), "", mcp.ToolCall{RoomID: "room-1"}, time.Second); err == nil {
		t.Error("a blank target user must be rejected")
	}
	if _, err := model.CallClientToolForUser(context.Background(), "admin-1", mcp.ToolCall{}, time.Second); err == nil {
		t.Error("a blank room id must be rejected")
	}
	if pendingClientCallCount(model) != 0 {
		t.Error("a rejected call must not be registered")
	}
}

func TestUserClientRPCSenderReachesTheTargetedUser(t *testing.T) {
	model := newAIAgentTestModelWithNats(newAIAgentTestRedis(t))
	ctx := context.Background()

	sender := model.NewUserClientRPCSender("admin-1")
	if sender == nil {
		t.Fatal("the sender must be wired to the model")
	}

	done := make(chan clientCallOutcome, 1)
	go func() {
		res, err := sender.CallClientTool(ctx, mcp.ToolCall{
			RoomID:   "room-1",
			ToolName: mcp.ToolNotepadWrite,
		}, 5*time.Second)
		done <- clientCallOutcome{res: res, err: err}
	}()

	callID := waitForPendingClientCall(t, model)
	model.HandleClientToolResult(ctx, "room-1", "admin-1", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"ok":true}`,
	})

	select {
	case out := <-done:
		if out.err != nil || out.res.OutputJSON != `{"ok":true}` {
			t.Fatalf("the sender = (%+v, %v), want the browser's result", out.res, out.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the sender did not resolve its call")
	}
}

func TestCallClientToolForUserStopsWhenTheContextIsDone(t *testing.T) {
	model := newAIAgentTestModelWithNats(newAIAgentTestRedis(t))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan clientCallOutcome, 1)
	go func() {
		res, err := model.CallClientToolForUser(ctx, "admin-1", mcp.ToolCall{
			RoomID:   "room-1",
			ToolName: mcp.ToolNotepadRead,
		}, time.Minute)
		done <- clientCallOutcome{res: res, err: err}
	}()

	waitForPendingClientCall(t, model)
	cancel()

	select {
	case out := <-done:
		if out.err == nil {
			t.Fatalf("a cancelled request must fail, got %+v", out.res)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled call did not return")
	}
	if pendingClientCallCount(model) != 0 {
		t.Error("a cancelled call must be removed from the pending calls")
	}
}
