package models

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// TestBreakoutBlocksReturnToMainRoom locks in that a per-user 1-on-1 AI session
// never lets its learner back into the parent room, while a normal-session
// breakout room only blocks an early return, never the return after it ended.
func TestBreakoutBlocksReturnToMainRoom(t *testing.T) {
	childMeta := func(allowReturn, allowSelfSelect, agentAllowed bool) *plugnmeet.RoomMetadata {
		return &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{
				AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{IsAllow: agentAllowed},
			},
			BreakoutRoomFeatures: &plugnmeet.BreakoutRoomFeatures{
				AllowReturnToMainRoom: allowReturn,
				AllowSelfSelect:       allowSelfSelect,
			},
		}}
	}
	parentMeta := func(mode plugnmeet.AIAgentSessionMode, allowedIds ...string) *plugnmeet.RoomMetadata {
		return &plugnmeet.RoomMetadata{RoomFeatures: &plugnmeet.RoomCreateFeatures{
			InsightsFeatures: &plugnmeet.InsightsFeatures{
				AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
					IsAllow:     mode != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM,
					SessionMode: mode,
					AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
						AllowedParticipantIds: allowedIds,
					},
				},
			},
		}}
	}
	normal := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM
	controlled := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS
	openPerUser := plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_OPEN_PER_USER
	// a child that carries the per-user AI session marker in its own metadata,
	// while still allowing a return, so only the marker can block it
	perUserChildMeta := func() *plugnmeet.RoomMetadata {
		meta := childMeta(true, false, true)
		meta.RoomFeatures.BreakoutRoomFeatures.IsPerUserAiSession = true
		return meta
	}

	tests := []struct {
		name        string
		parent      *plugnmeet.RoomMetadata
		child       *plugnmeet.RoomMetadata
		childActive bool
		want        bool
	}{
		{name: "missing child metadata", parent: parentMeta(normal), child: nil, childActive: true, want: false},
		{name: "normal room, returning allowed", parent: parentMeta(normal), child: childMeta(true, false, false), childActive: true, want: false},
		{name: "normal room, returning disabled", parent: parentMeta(normal), child: childMeta(false, false, false), childActive: true, want: true},
		{name: "normal room, self-select allowed", parent: parentMeta(normal), child: childMeta(false, true, false), childActive: true, want: false},
		{name: "normal room, breakout room ended", parent: parentMeta(normal), child: childMeta(false, false, false), childActive: false, want: false},
		{name: "normal room, AI breakout room still running", parent: parentMeta(normal), child: childMeta(false, false, true), childActive: true, want: true},
		{name: "normal room, AI breakout room ended", parent: parentMeta(normal), child: childMeta(false, false, true), childActive: false, want: false},
		{name: "normal room, child marked as a per-user AI session", parent: parentMeta(normal), child: perUserChildMeta(), childActive: true, want: true},
		{name: "normal room, per-user AI session child ended", parent: parentMeta(normal), child: perUserChildMeta(), childActive: false, want: true},
		{name: "open per-user session", parent: parentMeta(openPerUser), child: childMeta(true, false, true), childActive: true, want: true},
		{name: "open per-user session, room ended", parent: parentMeta(openPerUser), child: childMeta(false, false, true), childActive: false, want: true},
		{name: "controlled users with several participants", parent: parentMeta(controlled, "u1", "u2"), child: childMeta(true, false, true), childActive: true, want: true},
		{name: "controlled users with a single participant", parent: parentMeta(controlled, "u1"), child: childMeta(true, false, true), childActive: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := breakoutBlocksReturnToMainRoom(tt.parent, tt.child, tt.childActive); got != tt.want {
				t.Fatalf("breakoutBlocksReturnToMainRoom() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBreakoutChildRoomId(t *testing.T) {
	tests := []struct {
		name     string
		roomId   string
		parentId string
		want     bool
	}{
		{name: "direct child", roomId: "room-1", parentId: "room", want: true},
		{name: "multi digit index", roomId: "room-12", parentId: "room", want: true},
		{name: "hyphenated parent", roomId: "room-1-2", parentId: "room-1", want: true},
		{name: "nested suffix is not a number", roomId: "room-1-2", parentId: "room", want: false},
		{name: "same room", roomId: "room", parentId: "room", want: false},
		{name: "sibling parent", roomId: "room-1-2", parentId: "other", want: false},
		{name: "empty parent", roomId: "room-1", parentId: "", want: false},
		{name: "empty room id", roomId: "", parentId: "room", want: false},
		{name: "zero index", roomId: "room-0", parentId: "room", want: false},
		{name: "empty index", roomId: "room-", parentId: "room", want: false},
		{name: "negative index", roomId: "room--1", parentId: "room", want: false},
		{name: "non numeric index", roomId: "room-abc", parentId: "room", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBreakoutChildRoomId(tt.roomId, tt.parentId); got != tt.want {
				t.Fatalf("isBreakoutChildRoomId(%q, %q) = %v, want %v", tt.roomId, tt.parentId, got, tt.want)
			}
		})
	}
}
