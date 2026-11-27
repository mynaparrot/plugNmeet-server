package insightsservice

import (
	"context"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/fake"
)

// webpFrame is a minimal RIFF/WEBP header.
func webpFrame() []byte {
	return []byte("RIFF\x00\x00\x00\x00WEBP")
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
