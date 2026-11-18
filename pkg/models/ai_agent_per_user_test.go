package models

import (
	"fmt"
	"io"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
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
		{name: "controlled with one user", features: agentFeatures(true, modeControlled, controlledConfig("u1")), want: false},
		{name: "controlled with two users", features: agentFeatures(true, modeControlled, controlledConfig("u1", "u2")), want: true},
		{name: "controlled without users", features: agentFeatures(true, modeControlled, controlledConfig()), want: false},
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
			name:     "no limit anywhere",
			metadata: &plugnmeet.RoomMetadata{},
			want:     defaultMaxAIBreakoutSessions,
		},
		{
			name:     "allowed rooms only",
			metadata: withBreakouts(10, agentFeatures(true, modeOpenPerUse, nil)),
			want:     10,
		},
		{
			name:     "concurrent sessions cap wins",
			metadata: withBreakouts(10, agentFeatures(true, modeOpenPerUse, openPerUserConfig(3))),
			want:     3,
		},
		{
			name:     "concurrent sessions above the room limit is ignored",
			metadata: withBreakouts(2, agentFeatures(true, modeOpenPerUse, openPerUserConfig(9))),
			want:     2,
		},
		{
			name:     "a controlled roster never caps the sessions",
			metadata: withBreakouts(10, agentFeatures(true, modeControlled, controlledConfig("u1", "u2"))),
			want:     10,
		},
		{
			name:     "concurrent sessions in a normal room are ignored",
			metadata: withBreakouts(4, agentFeatures(true, modeNormalRoom, openPerUserConfig(2))),
			want:     4,
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
		{name: "controlled room with one participant", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1"))), want: true},
		{name: "controlled room with two participants", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("u1", "u2"))), want: false},
		{name: "controlled room without participants", meta: agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig())), want: false},
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

func TestValidateAIAgentSessionLimits(t *testing.T) {
	request := func(features *plugnmeet.InsightsAIAgentFeatures, allowedRooms uint32) *plugnmeet.CreateRoomReq {
		return &plugnmeet.CreateRoomReq{Metadata: &plugnmeet.RoomMetadata{
			RoomFeatures: &plugnmeet.RoomCreateFeatures{
				BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{IsAllow: true, AllowedNumberRooms: allowedRooms},
				InsightsFeatures:     &plugnmeet.InsightsFeatures{AiAgentFeatures: features},
			},
		}}
	}

	tests := []struct {
		name    string
		req     *plugnmeet.CreateRoomReq
		wantErr error
	}{
		{
			name: "normal room needs no sessions",
			req:  request(agentFeatures(true, modeNormalRoom, nil), 4),
		},
		{
			name:    "controlled users without a roster",
			req:     request(agentFeatures(true, modeControlled, nil), 4),
			wantErr: config.ErrAIAgentAllowedUsersRequired,
		},
		{
			name:    "controlled users with an empty roster",
			req:     request(agentFeatures(true, modeControlled, controlledConfig()), 4),
			wantErr: config.ErrAIAgentAllowedUsersRequired,
		},
		{
			name: "controlled users with a single participant",
			req:  request(agentFeatures(true, modeControlled, controlledConfig("u1")), 4),
		},
		{
			name: "controlled roster larger than the room limit is accepted",
			req:  request(agentFeatures(true, modeControlled, controlledConfig("u1", "u2", "u3", "u4", "u5")), 2),
		},
		{
			name: "open per user within the room limit",
			req:  request(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)), 2),
		},
		{
			name:    "open per user above the room limit",
			req:     request(agentFeatures(true, modeOpenPerUse, openPerUserConfig(3)), 2),
			wantErr: config.ErrAIAgentMaxConcurrentSessions,
		},
		{
			name: "open per user without a limit",
			req:  request(agentFeatures(true, modeOpenPerUse, nil), 1),
		},
	}

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	log := logger.WithField("test", t.Name())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &RoomModel{}
			err := m.validateAIAgentSessionLimits(tt.req, tt.req.GetMetadata().GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures(), log)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("validateAIAgentSessionLimits() = %v, want nil", err)
				}
				return
			}
			if err != tt.wantErr {
				t.Fatalf("validateAIAgentSessionLimits() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAIAgentSessionLimitsSkipsBreakoutRooms(t *testing.T) {
	req := &plugnmeet.CreateRoomReq{Metadata: &plugnmeet.RoomMetadata{
		IsBreakoutRoom: true,
		RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{AiAgentFeatures: agentFeatures(true, modeControlled, nil)},
		},
	}}

	logger := logrus.New()
	logger.SetOutput(io.Discard)

	m := &RoomModel{}
	if err := m.validateAIAgentSessionLimits(req, req.GetMetadata().GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures(), logger.WithField("test", t.Name())); err != nil {
		t.Fatalf("validateAIAgentSessionLimits() = %v, want nil for a breakout room", err)
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

	t.Run("a normal room is left alone", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeNormalRoom, controlledConfig("u1", "u2")))
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		features := req.GetMetadata().GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
		if features.GetAutoSessionConfig() != nil {
			t.Errorf("auto_session_config = %v, want it dropped", features.GetAutoSessionConfig())
		}
		if req.GetMetadata().GetDefaultLockSettings() != nil {
			t.Error("default lock settings were applied to a normal room")
		}
		if req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms were enabled for a normal room")
		}
	})

	t.Run("a controlled roster locks the device defaults", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeControlled, controlledConfig("u1")))
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		locks := req.GetMetadata().GetDefaultLockSettings()
		if !locks.GetLockWebcam() || !locks.GetLockScreenSharing() {
			t.Errorf("locks = %v, want webcam and screen sharing locked", locks)
		}
		if req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms were enabled for a single-participant room")
		}
	})

	t.Run("per-user sessions need breakout rooms", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)))
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		locks := req.GetMetadata().GetDefaultLockSettings()
		if !locks.GetLockWebcam() || !locks.GetLockScreenSharing() {
			t.Errorf("locks = %v, want webcam and screen sharing locked", locks)
		}
		if !req.GetMetadata().GetRoomFeatures().GetBreakoutRoomFeatures().GetIsAllow() {
			t.Error("breakout rooms were not enabled for per-user sessions")
		}
	})

	t.Run("an existing lock setting wins", func(t *testing.T) {
		req := newRequest(agentFeatures(true, modeControlled, controlledConfig("u1")))
		req.Metadata.DefaultLockSettings = &plugnmeet.LockSettings{LockWebcam: new(false)}
		(&RoomModel{}).applyAIAgentRoomDefaults(req)

		locks := req.GetMetadata().GetDefaultLockSettings()
		if locks.GetLockWebcam() {
			t.Error("lock_webcam = true, want the host value false")
		}
		if !locks.GetLockScreenSharing() {
			t.Error("lock_screen_sharing = false, want the default true")
		}
	})
}
