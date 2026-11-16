package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func preassigned(roomId string, userIds ...string) *plugnmeet.PreassignedBreakoutRoom {
	return &plugnmeet.PreassignedBreakoutRoom{RoomId: roomId, Title: roomId, UserIds: userIds}
}

func TestMergePreassignedBreakoutRooms(t *testing.T) {
	tests := []struct {
		name     string
		existing []*plugnmeet.PreassignedBreakoutRoom
		created  []*plugnmeet.PreassignedBreakoutRoom
		want     []string
	}{
		{
			name:     "no existing keeps created",
			existing: nil,
			created:  []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u1", "u1")},
			want:     []string{"room-u1"},
		},
		{
			name:     "second participant is appended",
			existing: []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u1", "u1")},
			created:  []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u2", "u2")},
			want:     []string{"room-u1", "room-u2"},
		},
		{
			name: "same room id is superseded without duplication",
			existing: []*plugnmeet.PreassignedBreakoutRoom{
				preassigned("room-1", "u1"),
				preassigned("room-u1", "u1"),
			},
			created: []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u1", "u1", "u1b")},
			want:    []string{"room-1", "room-u1"},
		},
		{
			name:     "nil entries are skipped",
			existing: []*plugnmeet.PreassignedBreakoutRoom{nil, preassigned("room-u1", "u1")},
			created:  []*plugnmeet.PreassignedBreakoutRoom{nil, preassigned("room-u2", "u2")},
			want:     []string{"room-u1", "room-u2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergePreassignedBreakoutRooms(tt.existing, tt.created)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d rooms, want %d", len(got), len(tt.want))
			}
			for i, id := range tt.want {
				if got[i].GetRoomId() != id {
					t.Fatalf("room %d: got %q, want %q", i, got[i].GetRoomId(), id)
				}
			}
		})
	}
}

func TestMergePreassignedBreakoutRoomsSupersedesContent(t *testing.T) {
	existing := []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u1", "u1")}
	created := []*plugnmeet.PreassignedBreakoutRoom{preassigned("room-u1", "u1", "u1b")}

	got := mergePreassignedBreakoutRooms(existing, created)
	if len(got) != 1 {
		t.Fatalf("got %d rooms, want 1", len(got))
	}
	if len(got[0].GetUserIds()) != 2 {
		t.Fatalf("got user ids %v, want the created entry to supersede the existing one", got[0].GetUserIds())
	}
}

func TestAppendBreakoutAIContext(t *testing.T) {
	tests := []struct {
		name          string
		basePrompt    string
		globalContext string
		roomContext   string
		want          string
	}{
		{
			name:       "no context keeps the parent prompt",
			basePrompt: "You are a tutor.",
			want:       "You are a tutor.",
		},
		{
			name:          "no context trims the parent prompt",
			basePrompt:    "  You are a tutor.  ",
			globalContext: "  ",
			want:          "You are a tutor.",
		},
		{
			name:          "global context only",
			basePrompt:    "You are a tutor.",
			globalContext: "Lesson: fractions",
			want:          "You are a tutor.\n\nBreakout Session Context:\nLesson: fractions",
		},
		{
			name:        "room context only",
			basePrompt:  "You are a tutor.",
			roomContext: "Learner: Ana",
			want:        "You are a tutor.\n\nBreakout Session Context:\nLearner: Ana",
		},
		{
			name:          "both contexts",
			basePrompt:    "You are a tutor.",
			globalContext: "Lesson: fractions",
			roomContext:   "Learner: Ana",
			want:          "You are a tutor.\n\nBreakout Session Context:\nLesson: fractions\nLearner: Ana",
		},
		{
			name:          "no parent prompt",
			globalContext: "Lesson: fractions",
			want:          "Breakout Session Context:\nLesson: fractions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendBreakoutAIContext(tt.basePrompt, tt.globalContext, tt.roomContext)
			if got != tt.want {
				t.Fatalf("appendBreakoutAIContext() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrepareAIAgentBreakoutChildKeepsParentPrompt(t *testing.T) {
	meta := &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
		InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
			IsAllow:               true,
			IsEnabled:             true,
			ActiveSessionId:       new("parent-session"),
			SystemPrompt:          "You are a tutor.",
			AllowedParticipantIds: []string{"user-1", "user-2"},
		}},
	}}
	// the create loop captures the parent features once, before any child exists
	parentAiFeatures := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()

	prepareAIAgentBreakoutChild(parentAiFeatures, meta, "user-1", "Lesson: fractions", "Learner: Ana")
	first := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
	prepareAIAgentBreakoutChild(parentAiFeatures, meta, "user-2", "Lesson: fractions", "Learner: Ben")
	second := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()

	wantFirst := "You are a tutor.\n\nBreakout Session Context:\nLesson: fractions\nLearner: Ana"
	if first.GetSystemPrompt() != wantFirst {
		t.Fatalf("first child prompt = %q, want %q", first.GetSystemPrompt(), wantFirst)
	}
	wantSecond := "You are a tutor.\n\nBreakout Session Context:\nLesson: fractions\nLearner: Ben"
	if second.GetSystemPrompt() != wantSecond {
		t.Fatalf("second child prompt = %q, want %q (it must not inherit the first child's context)", second.GetSystemPrompt(), wantSecond)
	}

	for userId, features := range map[string]*plugnmeet.InsightsAIAgentFeatures{"user-1": first, "user-2": second} {
		if !features.GetIsAllow() || features.GetIsEnabled() {
			t.Errorf("%s: is_allow=%v is_enabled=%v, want true/false", userId, features.GetIsAllow(), features.GetIsEnabled())
		}
		if features.GetActiveSessionId() != "" {
			t.Errorf("%s: active_session_id = %q, want it cleared", userId, features.GetActiveSessionId())
		}
		if features.GetAutoCreatePerUserSession() || !features.GetAutoJoinOnFirstParticipant() {
			t.Errorf("%s: auto_create_per_user_session=%v auto_join_on_first_participant=%v, want false/true", userId,
				features.GetAutoCreatePerUserSession(), features.GetAutoJoinOnFirstParticipant())
		}
		if ids := features.GetAllowedParticipantIds(); len(ids) != 1 || ids[0] != userId {
			t.Errorf("%s: allowed participant ids = %v, want only that user", userId, ids)
		}
	}
}
