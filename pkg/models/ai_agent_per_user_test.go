package models

import (
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func TestNormalizeAIAgentFeaturesKeepsAllowedParticipantIds(t *testing.T) {
	ids := []string{" learner-1 ", "learner-1", "learner-2", "", "   "}
	for i := 0; i < 20; i++ {
		ids = append(ids, strings.Repeat("x", 1)+string(rune('a'+i)))
	}

	out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
		IsAllow:               true,
		IsEnabled:             true,
		ActiveSessionId:       new("session-1"),
		AllowedParticipantIds: ids,
	})

	got := out.GetAllowedParticipantIds()
	if len(got) != aiAgentMaxAllowedParticipantIds {
		t.Fatalf("allowed participant ids = %d, want the cap of %d", len(got), aiAgentMaxAllowedParticipantIds)
	}
	if got[0] != "learner-1" || got[1] != "learner-2" {
		t.Errorf("ids = %v, want the trimmed and deduped list to start with learner-1, learner-2", got)
	}
	if out.GetIsEnabled() || out.GetActiveSessionId() != "" {
		t.Errorf("runtime state is_enabled=%v active_session_id=%q, want both cleared", out.GetIsEnabled(), out.GetActiveSessionId())
	}
}

func TestEffectiveMaxAISessions(t *testing.T) {
	tests := []struct {
		name     string
		metadata *plugnmeet.RoomMetadata
		want     uint32
	}{
		{
			name:     "no limit anywhere",
			metadata: &plugnmeet.RoomMetadata{},
			want:     defaultMaxAIBreakoutSessions,
		},
		{
			name: "allowed rooms only",
			metadata: &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: 10},
			}},
			want: 10,
		},
		{
			name: "concurrent sessions cap wins",
			metadata: &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: 10},
				InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
					MaxConcurrentSessions: 3,
				}},
			}},
			want: 3,
		},
		{
			name: "concurrent sessions above the room limit is ignored",
			metadata: &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: 2},
				InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
					MaxConcurrentSessions: 9,
				}},
			}},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveMaxAISessions(tt.metadata); got != tt.want {
				t.Fatalf("effectiveMaxAISessions() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestContainsUserId(t *testing.T) {
	ids := []string{"a", "b"}
	if !containsUserId(ids, "b") {
		t.Error("containsUserId() = false for a listed id")
	}
	if containsUserId(ids, "c") {
		t.Error("containsUserId() = true for an unlisted id")
	}
	if containsUserId(nil, "a") {
		t.Error("containsUserId() = true for an empty list")
	}
}

func TestIsOneOnOneAIAgentRoom(t *testing.T) {
	agentRoom := func(isAllow, perUser bool, ids []string) *plugnmeet.RoomMetadata {
		return &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
				IsAllow:                  isAllow,
				AutoCreatePerUserSession: perUser,
				AllowedParticipantIds:    ids,
			}},
		}}
	}
	breakout := func(meta *plugnmeet.RoomMetadata) *plugnmeet.RoomMetadata {
		meta.IsBreakoutRoom = true
		return meta
	}

	tests := []struct {
		name string
		meta *plugnmeet.RoomMetadata
		want bool
	}{
		{name: "no metadata", meta: nil, want: false},
		{name: "agent not allowed", meta: agentRoom(false, false, []string{"u1"}), want: false},
		{name: "breakout room with agent", meta: breakout(agentRoom(true, false, []string{"u1"})), want: true},
		{name: "breakout room without agent", meta: breakout(agentRoom(false, false, nil)), want: false},
		{name: "direct 1-on-1 room", meta: agentRoom(true, false, []string{"u1"}), want: true},
		{name: "group room with two participants", meta: agentRoom(true, false, []string{"u1", "u2"}), want: false},
		{name: "group room without participants", meta: agentRoom(true, false, nil), want: false},
		{name: "parent lobby of per-user sessions", meta: agentRoom(true, true, []string{"u1"}), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOneOnOneAIAgentRoom(tt.meta); got != tt.want {
				t.Fatalf("isOneOnOneAIAgentRoom() = %v, want %v", got, tt.want)
			}
		})
	}
}
