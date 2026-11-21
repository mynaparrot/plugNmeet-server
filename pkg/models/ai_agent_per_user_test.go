package models

import (
	"fmt"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

const (
	modeNormalRoom = plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM
	modeControlled = plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS
	modeOpenPerUse = plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER
)

// agentFeatures builds the AI agent features of a room for the given mode.
func agentFeatures(isAllow bool, mode plugnmeet.AIAgentSessionMode, cfg *plugnmeet.AIAgentAutoSessionConfig) *plugnmeet.InsightsAIAgentFeatures {
	return &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:           isAllow,
		SystemPrompt:      "You are a tutor.",
		Topic:             "fractions",
		SessionMode:       mode,
		AutoSessionConfig: cfg,
	}
}

// agentRoomMetadata wraps features into the room metadata the helpers expect.
func agentRoomMetadata(features *plugnmeet.InsightsAIAgentFeatures) *plugnmeet.RoomMetadata {
	return &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
		InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: features},
	}}
}

func controlledConfig(ids ...string) *plugnmeet.AIAgentAutoSessionConfig {
	return &plugnmeet.AIAgentAutoSessionConfig{AllowedParticipantIds: ids}
}

func openPerUserConfig(maxConc uint32) *plugnmeet.AIAgentAutoSessionConfig {
	return &plugnmeet.AIAgentAutoSessionConfig{MaxConcurrentSessions: maxConc}
}

func TestNormalizeAIAgentFeaturesPerSessionMode(t *testing.T) {
	// a roster that is longer than the protocol's max_items cap
	ids := []string{" learner-1 ", "learner-1", "learner-2", "", "   "}
	for i := 0; i < aiAgentMaxAllowedParticipantIds+8; i++ {
		ids = append(ids, fmt.Sprintf("learner-%d", i))
	}

	t.Run("normal room drops the routing config", func(t *testing.T) {
		out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
			IsAllow:           true,
			IsEnabled:         true,
			ActiveSessionId:   new("session-1"),
			SessionMode:       modeNormalRoom,
			AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{AllowedParticipantIds: ids},
		})

		if out.GetAutoSessionConfig() != nil {
			t.Fatalf("auto_session_config = %v, want nil", out.GetAutoSessionConfig())
		}
		if out.GetIsEnabled() || out.GetActiveSessionId() != "" {
			t.Errorf("runtime state is_enabled=%v active_session_id=%q, want both cleared", out.GetIsEnabled(), out.GetActiveSessionId())
		}
	})

	t.Run("controlled users keeps the cleaned roster only", func(t *testing.T) {
		out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
			IsAllow:     true,
			SessionMode: modeControlled,
			AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
				AllowedParticipantIds: ids,
				MaxConcurrentSessions: 5,
			},
		})

		got := out.GetAutoSessionConfig().GetAllowedParticipantIds()
		if len(got) != aiAgentMaxAllowedParticipantIds {
			t.Fatalf("allowed participant ids = %d, want the cap of %d", len(got), aiAgentMaxAllowedParticipantIds)
		}
		if got[0] != "learner-1" || got[1] != "learner-2" {
			t.Errorf("ids = %v, want the trimmed and deduped list to start with learner-1, learner-2", got)
		}
		if out.GetAutoSessionConfig().GetMaxConcurrentSessions() != 0 {
			t.Errorf("max_concurrent_sessions = %d, want it reset to 0 in controlled mode", out.GetAutoSessionConfig().GetMaxConcurrentSessions())
		}
		if out.GetAutoSessionConfig().AutoStartAgent == nil || !out.GetAutoSessionConfig().GetAutoStartAgent() {
			t.Error("auto_start_agent = nil/false, want the default true")
		}
	})

	t.Run("controlled users defaults the config in", func(t *testing.T) {
		out := NormalizeAIAgentFeatures(agentFeatures(true, modeControlled, nil))

		if out.GetAutoSessionConfig() == nil {
			t.Fatal("auto_session_config = nil, want it created")
		}
		if !out.GetAutoSessionConfig().GetAutoStartAgent() {
			t.Error("auto_start_agent = false, want the default true")
		}
	})

	t.Run("open per user drops the roster and keeps the limit", func(t *testing.T) {
		out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
			IsAllow:     true,
			SessionMode: modeOpenPerUse,
			AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
				AllowedParticipantIds: []string{"u1", "u2"},
				MaxConcurrentSessions: 4,
			},
		})

		if got := out.GetAutoSessionConfig().GetAllowedParticipantIds(); got != nil {
			t.Errorf("allowed participant ids = %v, want nil in open-per-user mode", got)
		}
		if got := out.GetAutoSessionConfig().GetMaxConcurrentSessions(); got != 4 {
			t.Errorf("max_concurrent_sessions = %d, want the host value 4", got)
		}
		if !out.GetAutoSessionConfig().GetAutoStartAgent() {
			t.Error("auto_start_agent = false, want the default true")
		}
	})

	t.Run("open per user above the cap is clamped", func(t *testing.T) {
		out := NormalizeAIAgentFeatures(agentFeatures(true, modeOpenPerUse, openPerUserConfig(utils.AIAgentMaxConcurrentSessionsCap+1)))

		if got := out.GetAutoSessionConfig().GetMaxConcurrentSessions(); got != utils.AIAgentMaxConcurrentSessionsCap {
			t.Errorf("max_concurrent_sessions = %d, want the cap of %d", got, utils.AIAgentMaxConcurrentSessionsCap)
		}
	})

	t.Run("an explicit auto_start_agent false is kept", func(t *testing.T) {
		for _, mode := range []plugnmeet.AIAgentSessionMode{modeControlled, modeOpenPerUse} {
			out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
				IsAllow:     true,
				SessionMode: mode,
				AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
					AllowedParticipantIds: []string{"u1"},
					AutoStartAgent:        new(false),
				},
			})
			if out.GetAutoSessionConfig().GetAutoStartAgent() {
				t.Errorf("%s: auto_start_agent = true, want the host value false", mode)
			}
		}
	})
}

func TestAIAgentSessionModeHelpers(t *testing.T) {
	t.Run("allowed participant ids", func(t *testing.T) {
		if got := aiAgentAllowedParticipantIds(nil); got != nil {
			t.Errorf("nil features = %v, want nil", got)
		}
		if got := aiAgentAllowedParticipantIds(agentFeatures(true, modeNormalRoom, controlledConfig("u1"))); got != nil {
			t.Errorf("normal room = %v, want nil", got)
		}
		if got := aiAgentAllowedParticipantIds(agentFeatures(true, modeOpenPerUse, controlledConfig("u1"))); got != nil {
			t.Errorf("open per user = %v, want nil", got)
		}
		got := aiAgentAllowedParticipantIds(agentFeatures(true, modeControlled, controlledConfig("u1", "u2")))
		if len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
			t.Errorf("controlled users = %v, want [u1 u2]", got)
		}
	})

	t.Run("max concurrent sessions", func(t *testing.T) {
		if got := aiAgentMaxConcurrentSessions(nil); got != 0 {
			t.Errorf("nil features = %d, want 0", got)
		}
		if got := aiAgentMaxConcurrentSessions(agentFeatures(true, modeNormalRoom, openPerUserConfig(3))); got != 0 {
			t.Errorf("normal room = %d, want 0", got)
		}
		if got := aiAgentMaxConcurrentSessions(agentFeatures(true, modeControlled, openPerUserConfig(3))); got != 0 {
			t.Errorf("controlled users = %d, want 0", got)
		}
		if got := aiAgentMaxConcurrentSessions(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))); got != 3 {
			t.Errorf("open per user = %d, want 3", got)
		}
	})

	t.Run("auto start", func(t *testing.T) {
		if aiAgentAutoStart(nil) {
			t.Error("nil features = true, want false")
		}
		if aiAgentAutoStart(agentFeatures(true, modeNormalRoom, nil)) {
			t.Error("normal room = true, want false (an admin starts the agent explicitly)")
		}
		for _, mode := range []plugnmeet.AIAgentSessionMode{modeControlled, modeOpenPerUse} {
			if !aiAgentAutoStart(agentFeatures(true, mode, nil)) {
				t.Errorf("%s: auto start = false, want the default true", mode)
			}
			if aiAgentAutoStart(agentFeatures(true, mode, &plugnmeet.AIAgentAutoSessionConfig{AutoStartAgent: new(false)})) {
				t.Errorf("%s: auto start = true, want the host value false", mode)
			}
			if !aiAgentAutoStart(agentFeatures(true, mode, &plugnmeet.AIAgentAutoSessionConfig{})) {
				t.Errorf("%s: auto start = false for an empty config, want the default true", mode)
			}
		}
	})
}

func TestIsAutoPerUserAISessionRoom(t *testing.T) {
	tests := []struct {
		name     string
		features *plugnmeet.InsightsAIAgentFeatures
		want     bool
	}{
		{name: "no features", features: nil, want: false},
		{name: "agent not allowed", features: agentFeatures(false, modeOpenPerUse, nil), want: false},
		{name: "normal room", features: agentFeatures(true, modeNormalRoom, nil), want: false},
		{name: "controlled with one user", features: agentFeatures(true, modeControlled, controlledConfig("u1")), want: true},
		{name: "controlled with two users", features: agentFeatures(true, modeControlled, controlledConfig("u1", "u2")), want: true},
		{name: "controlled without users", features: agentFeatures(true, modeControlled, controlledConfig()), want: false},
		{name: "controlled without a config", features: agentFeatures(true, modeControlled, nil), want: false},
		{name: "open per user", features: agentFeatures(true, modeOpenPerUse, nil), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAutoPerUserAISessionRoom(tt.features); got != tt.want {
				t.Fatalf("isAutoPerUserAISessionRoom() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEffectiveMaxAISessions(t *testing.T) {
	withBreakouts := func(allowedRooms uint32, features *plugnmeet.InsightsAIAgentFeatures) *plugnmeet.RoomMetadata {
		return &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
			BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: allowedRooms},
			InsightsFeatures:     &plugnmeet.InsightsFeatures{AiAgentFeatures: features},
		}}
	}

	tests := []struct {
		name     string
		metadata *plugnmeet.RoomMetadata
		want     uint32
	}{
		{
			name:     "no features at all",
			metadata: &plugnmeet.RoomMetadata{},
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name:     "open per user uses its configured sessions",
			metadata: withBreakouts(10, agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))),
			want:     3,
		},
		{
			name:     "allowed rooms below the sessions are ignored",
			metadata: withBreakouts(2, agentFeatures(true, modeOpenPerUse, openPerUserConfig(9))),
			want:     9,
		},
		{
			name:     "allowed rooms above the sessions are ignored",
			metadata: withBreakouts(20, agentFeatures(true, modeOpenPerUse, openPerUserConfig(2))),
			want:     2,
		},
		{
			name:     "open per user is capped by the server",
			metadata: withBreakouts(10, agentFeatures(true, modeOpenPerUse, openPerUserConfig(utils.AIAgentMaxConcurrentSessionsCap+4))),
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name:     "open per user without a limit gets the cap",
			metadata: withBreakouts(4, agentFeatures(true, modeOpenPerUse, nil)),
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name:     "a controlled roster gets the cap",
			metadata: withBreakouts(10, agentFeatures(true, modeControlled, controlledConfig("u1", "u2"))),
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name:     "a controlled roster ignores the allowed rooms",
			metadata: withBreakouts(2, agentFeatures(true, modeControlled, controlledConfig("u1", "u2"))),
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name:     "a normal room ignores the concurrent sessions",
			metadata: withBreakouts(4, agentFeatures(true, modeNormalRoom, openPerUserConfig(2))),
			want:     utils.AIAgentMaxConcurrentSessionsCap,
		},
		{
			name: "a room without breakout features gets the cap",
			metadata: &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
				InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: agentFeatures(true, modeControlled, controlledConfig("u1"))},
			}},
			want: utils.AIAgentMaxConcurrentSessionsCap,
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
		{name: "agent not allowed", meta: agentRoomMetadata(agentFeatures(false, modeControlled, controlledConfig("u1"))), want: false},
		{name: "breakout room with agent", meta: breakout(agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1")))), want: true},
		{name: "breakout room without agent", meta: breakout(agentRoomMetadata(agentFeatures(false, modeNormalRoom, nil))), want: false},
		// a parent lobby never hosts a session, whatever its mode
		{name: "controlled lobby with one participant", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1"))), want: false},
		{name: "controlled lobby with two participants", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1", "u2"))), want: false},
		{name: "controlled lobby without participants", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig())), want: false},
		{name: "normal room", meta: agentRoomMetadata(agentFeatures(true, modeNormalRoom, nil)), want: false},
		{name: "parent lobby of per-user sessions", meta: agentRoomMetadata(agentFeatures(true, modeOpenPerUse, nil)), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOneOnOneAIAgentRoom(tt.meta); got != tt.want {
				t.Fatalf("isOneOnOneAIAgentRoom() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsPerUserAISessionParent locks in that only the main room of an auto
// per-user AI session is treated as the owner of per-participant session rooms,
// so an unused pre-created room cannot keep it alive.
func TestIsPerUserAISessionParent(t *testing.T) {
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
		{name: "controlled lobby with a roster", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1"))), want: true},
		{name: "open per-user lobby", meta: agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))), want: true},
		{name: "normal room", meta: agentRoomMetadata(agentFeatures(true, modeNormalRoom, nil)), want: false},
		{name: "agent not allowed", meta: agentRoomMetadata(agentFeatures(false, modeOpenPerUse, openPerUserConfig(3))), want: false},
		{name: "session room itself", meta: breakout(agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3)))), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPerUserAISessionParent(tt.meta); got != tt.want {
				t.Fatalf("isPerUserAISessionParent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateAIAgentSessionMode(t *testing.T) {
	tests := []struct {
		name     string
		features *plugnmeet.InsightsAIAgentFeatures
		wantErr  error
	}{
		{name: "normal room", features: agentFeatures(true, modeNormalRoom, nil)},
		{name: "controlled users with a roster", features: agentFeatures(true, modeControlled, controlledConfig("u1"))},
		{
			name:     "controlled users without a roster",
			features: agentFeatures(true, modeControlled, nil),
			wantErr:  config.ErrAIAgentAllowedUsersRequired,
		},
		{
			name:     "controlled users with blank ids",
			features: agentFeatures(true, modeControlled, controlledConfig("  ", "")),
			wantErr:  config.ErrAIAgentAllowedUsersRequired,
		},
		{name: "open per user with a limit", features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))},
		{name: "open per user at the cap", features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(utils.AIAgentMaxConcurrentSessionsCap))},
		{
			name:     "open per user without a limit",
			features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(0)),
			wantErr:  config.ErrAIAgentMaxConcurrentSessions,
		},
		{
			// the value is clamped by normalizeAIAgentAutoSessionConfig
			name:     "open per user above the cap",
			features: agentFeatures(true, modeOpenPerUse, openPerUserConfig(utils.AIAgentMaxConcurrentSessionsCap+1)),
		},
		{
			name:     "an unknown mode",
			features: agentFeatures(true, plugnmeet.AIAgentSessionMode(9), nil),
			wantErr:  config.ErrAIAgentInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAIAgentSessionMode(tt.features)
			if err != tt.wantErr {
				t.Fatalf("ValidateAIAgentSessionMode() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestApplyAIAgentRoomDefaults(t *testing.T) {
	newRequest := func(features *plugnmeet.InsightsAIAgentFeatures) *plugnmeet.CreateRoomReq {
		return &plugnmeet.CreateRoomReq{Metadata: &plugnmeet.RoomMetadata{
			RoomFeatures: &plugnmeet.RoomCreateFeatures{
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: false},
				InsightsFeatures:     &plugnmeet.InsightsFeatures{AiAgentFeatures: features},
			},
		}}
	}

	t.Run("a normal room only gets its breakout rooms enabled", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeNormalRoom, controlledConfig("u1", "u2")))
		req.Metadata.RoomFeatures.AllowWebcams = true
		req.Metadata.RoomFeatures.AllowScreenShare = true
		req.Metadata.RoomFeatures.AllowRaiseHand = new(true)
		req.Metadata.RoomFeatures.AllowReactions = new(true)
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		features := req.GetMetadata().GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
		if features.GetAutoSessionConfig() != nil {
			t.Errorf("auto_session_config = %v, want it dropped", features.GetAutoSessionConfig())
		}
		if req.GetMetadata().GetDefaultLockSettings() != nil {
			t.Error("default lock settings were applied to a normal room")
		}
		roomFeatures := req.GetMetadata().GetRoomFeatures()
		if !roomFeatures.GetAllowWebcams() || !roomFeatures.GetAllowScreenShare() ||
			!roomFeatures.GetAllowRaiseHand() || !roomFeatures.GetAllowReactions() {
			t.Errorf("room features = %v, want the host values untouched", roomFeatures)
		}
		if !req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms = false, want them enabled for an AI agent room")
		}
	})

	t.Run("a controlled roster disables the unused room features", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeControlled, controlledConfig("u1")))
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		assertAIAgentSessionRoomFeatures(t, req.GetMetadata())
		if !req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms = false, want them enabled for an AI agent room")
		}
	})

	t.Run("a disabled breakout room is enabled without a room limit", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)))
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		assertAIAgentSessionRoomFeatures(t, req.GetMetadata())
		breakout := req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures()
		if !breakout.GetIsAllow() {
			t.Error("breakout rooms = false, want them enabled")
		}
		if breakout.GetAllowedNumberRooms() != 0 {
			t.Errorf("allowed_number_rooms = %d, want the host value 0 untouched", breakout.GetAllowedNumberRooms())
		}
	})

	t.Run("the host room limit is never touched", func(t *testing.T) {
		for _, allowed := range []uint32{2, 8} {
			req := newRequest(agentFeatures(true, modeOpenPerUse, openPerUserConfig(9)))
			req.Metadata.RoomFeatures.BreakoutRoomFeatures = &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: allowed}
			(&RoomModel{}).applyAIAgentRoomDefaults(req)

			if got := req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetAllowedNumberRooms(); got != allowed {
				t.Errorf("allowed_number_rooms = %d, want the host value %d", got, allowed)
			}
		}
	})

	t.Run("a breakout room keeps its features", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeOpenPerUse, openPerUserConfig(4)))
		req.Metadata.IsBreakoutRoom = true
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		if req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms were enabled inside a breakout room")
		}
	})

	t.Run("an active waiting room is disabled", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeControlled, controlledConfig("u1")))
		req.Metadata.RoomFeatures.WaitingRoomFeatures = &plugnmeet.WaitingRoomFeatures{IsActive: true}
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		assertAIAgentSessionRoomFeatures(t, req.GetMetadata())
	})

	t.Run("an existing lock setting wins", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeControlled, controlledConfig("u1")))
		req.Metadata.DefaultLockSettings = &plugnmeet.LockSettings{LockWebcam: new(true)}
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		locks := req.GetMetadata().GetDefaultLockSettings()
		if !locks.GetLockWebcam() {
			t.Error("lock_webcam = false, want the host value true")
		}
		if !locks.GetLockPrivateChat() {
			t.Error("lock_private_chat = false, want the default true")
		}
	})

	t.Run("a host unlocked workspace is locked again", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)))
		req.Metadata.DefaultLockSettings = &plugnmeet.LockSettings{
			LockWhiteboard:    new(false),
			LockSharedNotepad: new(false),
		}
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		locks := req.GetMetadata().GetDefaultLockSettings()
		if !locks.GetLockWhiteboard() {
			t.Error("lock_whiteboard = false, want the agent's locked default")
		}
		if !locks.GetLockSharedNotepad() {
			t.Error("lock_shared_notepad = false, want the agent's locked default")
		}
	})
}

// assertAIAgentSessionRoomFeatures checks the feature set an AI agent session
// room must carry: the unused features and the waiting room are disabled, and
// the private chat and both workspace surfaces are locked for everybody.
func assertAIAgentSessionRoomFeatures(t *testing.T, meta *plugnmeet.RoomMetadata) {
	t.Helper()
	rf := meta.GetRoomFeatures()
	if rf.GetAllowWebcams() {
		t.Error("allow_webcams = true, want it disabled")
	}
	if rf.GetWaitingRoomFeatures().GetIsActive() {
		t.Error("waiting_room_features.is_active = true, want it disabled")
	}
	if rf.GetAllowScreenShare() {
		t.Error("allow_screen_share = true, want it disabled")
	}
	if rf.GetAllowRaiseHand() {
		t.Error("allow_raise_hand = true, want it disabled")
	}
	if rf.GetAllowReactions() {
		t.Error("allow_reactions = true, want it disabled")
	}
	locks := meta.GetDefaultLockSettings()
	if !locks.GetLockPrivateChat() {
		t.Error("lock_private_chat = false, want it locked")
	}
	if locks.LockWhiteboard == nil || !locks.GetLockWhiteboard() {
		t.Error("lock_whiteboard = false, want it locked: only the agent hands out the pen")
	}
	if locks.LockSharedNotepad == nil || !locks.GetLockSharedNotepad() {
		t.Error("lock_shared_notepad = false, want it locked: only the agent hands out the pad")
	}
	if locks.GetLockWebcam() || locks.GetLockScreenSharing() {
		t.Errorf("locks = %v, want the webcam and screen share disabled via room features, not locked", locks)
	}
}
