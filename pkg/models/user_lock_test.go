package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func TestLockUpdateTargetsSkipsWhoCannotHoldALock(t *testing.T) {
	userIds := []string{"admin", "learner", "", config.AgentUserUserIdPrefix + "agent", config.IngressUserIdPrefix + "bot"}

	got := lockUpdateTargets(userIds, "admin")

	if len(got) != 1 || got[0] != "learner" {
		t.Fatalf("lockUpdateTargets() = %v, want only the human participant", got)
	}
}
