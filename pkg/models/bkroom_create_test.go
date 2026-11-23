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
			name:          "per-user context wins over the global context",
			basePrompt:    "You are a tutor.",
			globalContext: "Lesson: fractions",
			roomContext:   "Learner: Ana",
			want:          "You are a tutor.\n\nBreakout Session Context:\nLearner: Ana",
		},
		{
			name:          "blank per-user context falls back to the global context",
			basePrompt:    "You are a tutor.",
			globalContext: "Lesson: fractions",
			roomContext:   "  ",
			want:          "You are a tutor.\n\nBreakout Session Context:\nLesson: fractions",
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

func TestPerUserAIBreakoutRoomCarriesPreviousSessionContext(t *testing.T) {
	tests := []struct {
		name    string
		prevCtx string
		want    string
	}{
		{name: "context is carried", prevCtx: "Learner: Ana", want: "Learner: Ana"},
		{name: "context is trimmed", prevCtx: "  Learner: Ana  ", want: "Learner: Ana"},
		{name: "empty context stays unset", prevCtx: ""},
		{name: "whitespace context stays unset", prevCtx: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			room := perUserAIBreakoutRoom("user-1", "Ana", tt.prevCtx)
			if room.GetId() != "user-1" || room.GetTitle() != "Ana (AI Session)" {
				t.Fatalf("room = %q/%q, want user-1/Ana (AI Session)", room.GetId(), room.GetTitle())
			}
			if len(room.GetUsers()) != 1 || room.GetUsers()[0].GetId() != "user-1" {
				t.Fatalf("room users = %v, want only user-1", room.GetUsers())
			}
			if got := room.GetAiAgentContext(); got != tt.want {
				t.Fatalf("ai_agent_context = %q, want %q", got, tt.want)
			}
			if tt.want == "" && room.AiAgentContext != nil {
				t.Error("ai_agent_context must stay nil when there is no context")
			}
		})
	}
}

// TestApplyBreakoutChildRoomFeatures covers the difference between an auto
// per-user 1-on-1 AI session room and a regular breakout room: only the former
// keeps the learner inside and keeps the parent's logout url.
func TestApplyBreakoutChildRoomFeatures(t *testing.T) {
	parentFeatures := func(mode plugnmeet.AIAgentSessionMode, allowedIds ...string) *plugnmeet.InsightsAIAgentFeatures {
		return &plugnmeet.InsightsAIAgentFeatures{
			IsAllow:     mode != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM,
			SessionMode: mode,
			AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
				AllowedParticipantIds: allowedIds,
			},
		}
	}
	logoutUrl := "https://example.com/exit"
	normal := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM
	controlled := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS
	openPerUser := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER

	tests := []struct {
		name                  string
		req                   *plugnmeet.CreateBreakoutRoomsReq
		parent                *plugnmeet.InsightsAIAgentFeatures
		wantAllowReturnToMain bool
		wantAllowSelfSelect   bool
		wantIsPerUserAiSess   bool
		wantLogoutUrl         string
	}{
		{
			name: "normal breakout room",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: true,
				AllowSelfSelect:       true,
			},
			parent:                parentFeatures(normal),
			wantAllowReturnToMain: true,
			wantAllowSelfSelect:   true,
		},
		{
			name: "normal-session AI breakout room keeps returning allowed",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: true,
				EnableAiAgent:         true,
			},
			parent:                parentFeatures(normal),
			wantAllowReturnToMain: true,
			wantAllowSelfSelect:   false,
		},
		{
			name: "normal-session AI breakout room keeps returning disabled",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: false,
				EnableAiAgent:         true,
			},
			parent:                parentFeatures(normal),
			wantAllowReturnToMain: false,
			wantAllowSelfSelect:   false,
		},
		{
			name: "auto per-user AI session room",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: true,
				EnableAiAgent:         true,
			},
			parent:                parentFeatures(openPerUser),
			wantAllowReturnToMain: false,
			wantAllowSelfSelect:   false,
			wantIsPerUserAiSess:   true,
			wantLogoutUrl:         logoutUrl,
		},
		{
			name: "controlled users with several participants is a per-user session",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: true,
				EnableAiAgent:         true,
			},
			parent:                parentFeatures(controlled, "u1", "u2"),
			wantAllowReturnToMain: false,
			wantAllowSelfSelect:   false,
			wantIsPerUserAiSess:   true,
			wantLogoutUrl:         logoutUrl,
		},
		{
			name: "controlled users with a single participant is a per-user session",
			req: &plugnmeet.CreateBreakoutRoomsReq{
				AllowReturnToMainRoom: true,
				EnableAiAgent:         true,
			},
			parent:                parentFeatures(controlled, "u1"),
			wantAllowReturnToMain: false,
			wantAllowSelfSelect:   false,
			wantIsPerUserAiSess:   true,
			wantLogoutUrl:         logoutUrl,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &plugnmeet.RoomMetadata{
				LogoutUrl:    new(logoutUrl),
				RoomFeatures: &plugnmeet.RoomCreateFeatures{},
			}
			isPerUserAISession := tt.req.GetEnableAiAgent() && isAutoPerUserAISessionRoom(tt.parent)

			applyBreakoutChildRoomFeatures(meta, tt.req, isPerUserAISession)

			features := meta.GetRoomFeatures().GetBreakoutRoomFeatures()
			if features.GetIsAllow() {
				t.Error("a breakout child room must not host its own breakout rooms")
			}
			if got := features.GetAllowReturnToMainRoom(); got != tt.wantAllowReturnToMain {
				t.Errorf("allow_return_to_main_room = %v, want %v", got, tt.wantAllowReturnToMain)
			}
			if got := features.GetAllowSelfSelect(); got != tt.wantAllowSelfSelect {
				t.Errorf("allow_self_select = %v, want %v", got, tt.wantAllowSelfSelect)
			}
			if got := features.GetIsPerUserAiSession(); got != tt.wantIsPerUserAiSess {
				t.Errorf("is_per_user_ai_session = %v, want %v", got, tt.wantIsPerUserAiSess)
			}
			if got := meta.GetLogoutUrl(); got != tt.wantLogoutUrl {
				t.Errorf("logout_url = %q, want %q", got, tt.wantLogoutUrl)
			}
		})
	}
}

func TestPrepareAIAgentBreakoutChildKeepsParentPrompt(t *testing.T) {
	// the create loop marks the shared child metadata as a breakout room first
	meta := &plugnmeet.RoomMetadata{IsBreakoutRoom: true, RoomFeatures: &plugnmeet.RoomCreateFeatures{
		AllowWebcams:     true,
		AllowScreenShare: true,
		AllowRaiseHand:   new(true),
		AllowReactions:   new(true),
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

	// the per-user context wins, so the global context is dropped for each child
	wantFirst := "You are a tutor.\n\nBreakout Session Context:\nLearner: Ana"
	if first.GetSystemPrompt() != wantFirst {
		t.Fatalf("first child prompt = %q, want %q", first.GetSystemPrompt(), wantFirst)
	}
	wantSecond := "You are a tutor.\n\nBreakout Session Context:\nLearner: Ben"
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
		if !aiAgentAutoStart(features) {
			t.Errorf("%s: auto_start_agent = false, want the inherited default true", userId)
		}
		if ids := aiAgentAllowedParticipantIds(features); len(ids) != 1 || ids[0] != userId {
			t.Errorf("%s: allowed participant ids = %v, want only that user", userId, ids)
		}
	}
	if !isOneOnOneAIAgentRoom(meta) {
		t.Error("the child room is not a 1-on-1 AI session room")
	}
	assertAIAgentSessionRoomFeatures(t, meta)
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
				&plugnmeet.AIAgentAutoSessionConfig{AutoStartAgent: new(false)}),
			wantStart: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				AllowWebcams:     true,
				AllowScreenShare: true,
				AllowRaiseHand:   new(true),
				AllowReactions:   new(true),
				InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: tt.parent},
			}}

			prepareAIAgentBreakoutChild(tt.parent, meta, "user-1", "", "")

			assertAIAgentSessionRoomFeatures(t, meta)
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
