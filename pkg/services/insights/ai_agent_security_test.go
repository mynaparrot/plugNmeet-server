package insightsservice

import (
	"context"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/fake"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
)

// webpFrame is a minimal RIFF/WEBP header.
func webpFrame() []byte {
	return []byte("RIFF\x00\x00\x00\x00WEBP")
}

func pendingCallCount(a *AIAgent) int {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()

	return len(a.pendingCalls)
}

// TestAIAgentThrottlesAutomaticVisionPushes locks in that a participant drawing
// quickly cannot flood the session, while explicit snapshots always arrive.
func TestAIAgentThrottlesAutomaticVisionPushes(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	push := func(reason string) {
		t.Helper()
		if err := env.agent.HandleWhiteboardScene(&plugnmeet.AIAgentWhiteboardSceneMsg{
			RoomId:         "room",
			Reason:         reason,
			WebpImage:      webpFrame(),
			SceneGraphJson: `{"elements":[]}`,
		}); err != nil {
			t.Fatalf("handle whiteboard scene: %v", err)
		}
	}

	push("participant_edit")
	if got := len(env.session.Images()); got != 1 {
		t.Fatalf("images = %d, want 1", got)
	}

	push("participant_edit")
	if got := len(env.session.Images()); got != 1 {
		t.Fatalf("images = %d, want the second automatic push to be dropped", got)
	}

	push("tool_request")
	push("page_change")
	if got := len(env.session.Images()); got != 3 {
		t.Fatalf("images = %d, want the explicit pushes to be delivered", got)
	}
}

// TestAIAgentClientToolResultMustComeFromTheTargetUser locks in that a tool call
// is bound to the participant it was dispatched to.
func TestAIAgentClientToolResultMustComeFromTheTargetUser(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	type outcome struct {
		res mcp.ToolResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := env.agent.CallClientTool(context.Background(), mcp.ToolCall{
			RoomID:    "room",
			ToolName:  "whiteboard_draw_shapes",
			ArgsJSON:  `{}`,
			SessionID: "session-1",
		}, 5*time.Second)
		done <- outcome{res: res, err: err}
	}()

	var callID string
	waitFor(t, func() bool {
		calls := env.transport.toolCalls()
		if len(calls) == 0 {
			return false
		}
		callID = calls[0].GetCallId()
		return callID != ""
	}, "client tool dispatched")
	// The provider left the id empty, so one is generated before dispatching.
	if callID == "" {
		t.Fatal("a call id must be generated when the model left it empty")
	}

	if env.agent.HandleClientToolResultFromUser("intruder", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"ids":["evil"]}`,
	}) {
		t.Error("a result from a participant that does not own the call must be rejected")
	}
	if got := pendingCallCount(env.agent); got != 1 {
		t.Errorf("pending calls = %d, want the rejected result to keep the call in flight", got)
	}

	if !env.agent.HandleClientToolResultFromUser("user-1", &plugnmeet.AIAgentToolResultMsg{
		CallId: callID, Ok: true, ResultJson: `{"ids":["a"]}`,
	}) {
		t.Error("the targeted participant's result must be accepted")
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("client tool call: %v", out.err)
		}
		if out.res.OutputJSON != `{"ids":["a"]}` {
			t.Errorf("output = %q, want the targeted participant's result", out.res.OutputJSON)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the targeted call was not resolved")
	}
	if got := pendingCallCount(env.agent); got != 0 {
		t.Errorf("pending calls = %d, want the resolved call to be removed", got)
	}
}

// TestAIAgentGivesUpWhenTheProviderKeepsClosingTheSession locks in that a
// flapping provider stops the agent instead of redialling forever.
func TestAIAgentGivesUpWhenTheProviderKeepsClosingTheSession(t *testing.T) {
	next := fake.NewSession()
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.NewSession = func(context.Context) (insights.RealtimeSession, error) { return next, nil }
	})

	// The provider already closed the allowed number of fresh sessions.
	env.agent.redialMu.Lock()
	env.agent.consecutiveRedials = maxConsecutiveRedials
	env.agent.sessionOpenedAt = time.Now()
	env.agent.redialMu.Unlock()

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventGoAway})

	select {
	case <-env.agent.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the agent must stop instead of redialling forever")
	}
	if env.agent.currentSession() != env.session {
		t.Error("no new session may be opened once the redial budget is spent")
	}
}

// TestAIAgentBacksOffBeforeRediallingAgain locks in the wait between rapid
// redials, so a flapping provider is not hammered.
func TestAIAgentBacksOffBeforeRediallingAgain(t *testing.T) {
	next := fake.NewSession()
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.NewSession = func(context.Context) (insights.RealtimeSession, error) { return next, nil }
	})

	// One redial already happened, so the next one has to wait.
	env.agent.redialMu.Lock()
	env.agent.consecutiveRedials = 1
	env.agent.sessionOpenedAt = time.Now()
	env.agent.redialMu.Unlock()

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventGoAway})

	time.Sleep(200 * time.Millisecond)
	if env.agent.currentSession() == next {
		t.Fatal("a rapid second go away must wait before redialling")
	}
	waitFor(t, func() bool {
		return env.agent.currentSession() == next
	}, "session replaced after the backoff")
}
