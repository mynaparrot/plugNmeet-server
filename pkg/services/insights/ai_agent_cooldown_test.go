package insightsservice

import (
	"strings"
	"testing"
)

// TestAIAgentCooldownsBursts locks in that a burst of typed messages or server
// notes cannot force a model turn each. A throttled note still reaches the
// record, it just does not ask for a reply.
func TestAIAgentCooldownsBursts(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.HandleParticipantChatMessage("user-7", "Sam", "first"); err != nil {
		t.Fatalf("first chat message: %v", err)
	}
	if err := env.agent.HandleParticipantChatMessage("user-7", "Sam", "second"); err == nil {
		t.Fatal("a chat message inside the cooldown must not force another turn")
	}

	if err := env.agent.SendProactiveContext("The countdown timer has finished.", "countdown_timer"); err != nil {
		t.Fatalf("first proactive note: %v", err)
	}
	if err := env.agent.SendProactiveContext("The poll has closed.", "poll_closed"); err != nil {
		t.Fatalf("a throttled proactive note must not fail: %v", err)
	}

	contexts := env.session.Contexts()
	last := contexts[len(contexts)-1]
	if last.TriggerResponse || !strings.Contains(last.Text, "The poll has closed.") {
		t.Errorf("throttled note = %+v, want it delivered without a forced turn", last)
	}
}
