package config

import "testing"

// TestIntegratorAIAgentCreateRoomMsgs proves a CreateRoom refusal of an
// ai_agent_features block reaches an integrator as curated English instead of
// the generic fallback.
func TestIntegratorAIAgentCreateRoomMsgs(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrAIAgentNotAllowed, MsgAuthAIAgentNotAllowed},
		{ErrAIAgentE2EEUnsupported, MsgAuthAIAgentE2EEUnsupported},
		{ErrAIAgentPromptRequired, MsgAuthAIAgentPromptRequired},
		{ErrAIAgentTopicRequired, MsgAuthAIAgentTopicRequired},
		{ErrAIAgentProviderMissing, MsgAuthAIAgentProviderMissing},
		{ErrAIAgentInvalidMCPServer, MsgAuthAIAgentInvalidMCPServer},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			got := IntegratorMsg(tt.err.Error())
			if got != tt.want {
				t.Fatalf("IntegratorMsg(%q) = %q, want %q", tt.err.Error(), got, tt.want)
			}
			if got == MsgIntegratorUnexpected {
				t.Fatalf("%q fell back to the generic integrator message", tt.err)
			}
		})
	}
}
