package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// TestAdminAISessionPreCreate locks in that an admin's own 1-on-1 room is only
// prepared when the room runs auto per-user sessions and the admin is explicitly
// pre-authorized: an observer admin must not hold a concurrent session slot.
func TestAdminAISessionPreCreate(t *testing.T) {
	tests := []struct {
		name     string
		features *plugnmeet.InsightsAIAgentFeatures
		userId   string
		want     bool
	}{
		{name: "nil features", features: nil, userId: "admin-1", want: false},
		{name: "normal room", features: agentFeatures(true, modeNormalRoom, controlledConfig("admin-1")), userId: "admin-1", want: false},
		{name: "open per user is never explicit", features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(3)), userId: "admin-1", want: false},
		{name: "controlled single user pre-authorizes that user", features: agentFeatures(true, modeControlled, controlledConfig("admin-1")), userId: "admin-1", want: true},
		{name: "controlled single user omits the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1")), userId: "admin-1", want: false},
		{name: "controlled roster lists the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1", "admin-1")), userId: "admin-1", want: true},
		{name: "controlled roster omits the admin", features: agentFeatures(true, modeControlled, controlledConfig("learner-1", "learner-2")), userId: "admin-1", want: false},
		{name: "agent not allowed", features: agentFeatures(false, modeControlled, controlledConfig("learner-1", "admin-1")), userId: "admin-1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := adminAISessionPreCreate(tt.features, tt.userId); got != tt.want {
				t.Fatalf("adminAISessionPreCreate() = %v, want %v", got, tt.want)
			}
		})
	}
}
