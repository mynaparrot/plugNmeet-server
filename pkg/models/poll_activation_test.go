package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func pollsRoomMetadata(isAllow, isActive bool) *plugnmeet.RoomMetadata {
	return &plugnmeet.RoomMetadata{
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			PollsFeatures: &plugnmeet.PollsFeatures{
				IsAllow:  isAllow,
				IsActive: isActive,
			},
		},
	}
}

// TestEnsurePollsFeatureActive covers the rule that a poll or quiz created by
// the agent turns the room's polls feature on before it is created, so the
// participants can see the Polls icon and panel.
func TestEnsurePollsFeatureActive(t *testing.T) {
	t.Run("inactive polls are activated", func(t *testing.T) {
		var activated *plugnmeet.ActivatePollsReq
		activate := func(req *plugnmeet.ActivatePollsReq) error {
			activated = req
			return nil
		}

		err := ensurePollsFeatureActive("room-1", pollsRoomMetadata(true, false), activate)
		if err != nil {
			t.Fatalf("ensurePollsFeatureActive returned %v", err)
		}
		if activated == nil || activated.GetRoomId() != "room-1" || !activated.GetIsActive() {
			t.Fatalf("activation = %+v, want room-1 is_active=true", activated)
		}
	})

	t.Run("already active polls are left alone", func(t *testing.T) {
		called := false
		activate := func(*plugnmeet.ActivatePollsReq) error {
			called = true
			return nil
		}

		if err := ensurePollsFeatureActive("room-1", pollsRoomMetadata(true, true), activate); err != nil {
			t.Fatalf("ensurePollsFeatureActive returned %v", err)
		}
		if called {
			t.Fatal("an active polls feature must not be activated again")
		}
	})

	t.Run("polls disabled by the room configuration", func(t *testing.T) {
		called := false
		activate := func(*plugnmeet.ActivatePollsReq) error {
			called = true
			return nil
		}

		err := ensurePollsFeatureActive("room-1", pollsRoomMetadata(false, false), activate)
		if err == nil || !strings.Contains(err.Error(), "polls are disabled") {
			t.Fatalf("error = %v, want polls-disabled", err)
		}
		if called {
			t.Fatal("a disabled polls feature must not be activated")
		}
	})

	t.Run("missing polls features are disabled", func(t *testing.T) {
		err := ensurePollsFeatureActive("room-1", &plugnmeet.RoomMetadata{}, func(*plugnmeet.ActivatePollsReq) error {
			t.Fatal("no activation for missing features")
			return nil
		})
		if err == nil {
			t.Fatal("missing polls features must be refused")
		}
	})

	t.Run("activation errors are returned", func(t *testing.T) {
		wantErr := errors.New("nats is down")
		err := ensurePollsFeatureActive("room-1", pollsRoomMetadata(true, false), func(*plugnmeet.ActivatePollsReq) error {
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
	})
}

// TestEnsurePollsActiveWithoutServices keeps unit tests without a NATS room
// working: there is no feature switch to flip, so the poll creation proceeds.
func TestEnsurePollsActiveWithoutServices(t *testing.T) {
	if err := (aiAgentPollBackend{}).ensurePollsActive("room-1"); err != nil {
		t.Fatalf("ensurePollsActive without a model = %v, want nil", err)
	}
	if err := (aiAgentPollBackend{m: &AIAgentModel{}}).ensurePollsActive("room-1"); err != nil {
		t.Fatalf("ensurePollsActive without services = %v, want nil", err)
	}
}
