package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
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
			IsAllow:         true,
			IsEnabled:       true,
			ActiveSessionId: new("parent-session"),
			SystemPrompt:    "You are a tutor.",
			SessionMode:     plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS,
			AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
				AllowedParticipantIds: []string{"user-1", "user-2"},
			},
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
		if features.GetSessionMode() != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS {
			t.Errorf("%s: session_mode = %v, want CONTROLLED_USERS", userId, features.GetSessionMode())
		}
		if isAutoPerUserAISessionRoom(features) {
			t.Errorf("%s: the child room is not a per-user room anymore", userId)
		}
		if !aiAgentAutoStart(features) {
			t.Errorf("%s: auto_start_agent = false, want the inherited default true", userId)
		}
		if ids := aiAgentAllowedParticipantIds(features); len(ids) != 1 || ids[0] != userId {
			t.Errorf("%s: allowed participant ids = %v, want only that user", userId, ids)
		}
	}
}

func TestPrepareAIAgentBreakoutChildAutoStart(t *testing.T) {
	parent := func(mode plugnmeet.AIAgentSessionMode, cfg *plugnmeet.AIAgentAutoSessionConfig) *plugnmeet.InsightsAIAgentFeatures {
		return &plugnmeet.InsightsAIAgentFeatures{
			IsAllow:           true,
			SystemPrompt:      "You are a tutor.",
			SessionMode:       mode,
			AutoSessionConfig: cfg,
		}
	}

	tests := []struct {
		name      string
		parent    *plugnmeet.InsightsAIAgentFeatures
		wantStart bool
	}{
		{
			name:      "normal room defaults to auto start",
			parent:    parent(plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM, nil),
			wantStart: true,
		},
		{
			name:      "controlled users defaults to auto start",
			parent:    parent(plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS, nil),
			wantStart: true,
		},
		{
			name:      "open per user defaults to auto start",
			parent:    parent(plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER, nil),
			wantStart: true,
		},
		{
			name: "an explicit auto start false is inherited",
			parent: parent(plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER,
				&plugnmeet.AIAgentAutoSessionConfig{AutoStartAgent: proto.Bool(false)}),
			wantStart: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: tt.parent},
			}}

			prepareAIAgentBreakoutChild(tt.parent, meta, "user-1", "", "")

			child := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
			if child.GetSessionMode() != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS {
				t.Fatalf("child session_mode = %v, want CONTROLLED_USERS", child.GetSessionMode())
			}
			cfg := child.GetAutoSessionConfig()
			if cfg == nil || cfg.AutoStartAgent == nil {
				t.Fatalf("child auto_session_config = %v, want auto_start_agent to be set", cfg)
			}
			if got := cfg.GetAutoStartAgent(); got != tt.wantStart {
				t.Errorf("child auto_start_agent = %v, want %v", got, tt.wantStart)
			}
			if ids := cfg.GetAllowedParticipantIds(); len(ids) != 1 || ids[0] != "user-1" {
				t.Errorf("child allowed participant ids = %v, want [user-1]", ids)
			}
			// the parent's config must never be mutated
			if tt.parent.GetAutoSessionConfig() == cfg {
				t.Error("the child reused the parent's auto_session_config instead of its own copy")
			}
		})
	}
}
