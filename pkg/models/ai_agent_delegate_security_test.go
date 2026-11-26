package models

import (
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// A 1-on-1 room names its participants: an unlisted user may never be picked,
// even when they are the only non-admin human online.
func TestPickDelegateKeepsUnauthorizedUsersOutOfAOneOnOneRoom(t *testing.T) {
	allowed := []string{"learner-1"}

	onlyStranger := []*plugnmeet.NatsKvUserInfo{{UserId: "stranger"}}
	if got := pickDelegate(onlyStranger, allowed); got != nil {
		t.Fatalf("delegate = %q, want none", got.GetUserId())
	}

	// An admin supervisor may stand in, but never wins over the named participant.
	supervisor := []*plugnmeet.NatsKvUserInfo{
		{UserId: "stranger"},
		{UserId: "teacher", IsAdmin: true},
	}
	if got := pickDelegate(supervisor, allowed); got.GetUserId() != "teacher" {
		t.Errorf("delegate = %q, want the admin supervisor", got.GetUserId())
	}

	withLearner := append([]*plugnmeet.NatsKvUserInfo{{UserId: "learner-1"}}, supervisor...)
	if got := pickDelegate(withLearner, allowed); got.GetUserId() != "learner-1" {
		t.Errorf("delegate = %q, want the named participant", got.GetUserId())
	}
}

func TestPickDelegateFallsBackToTheFirstOnlineHumanInAGroupRoom(t *testing.T) {
	if got := pickDelegate(nil, nil); got != nil {
		t.Errorf("delegate = %q, want none for an empty room", got.GetUserId())
	}

	humans := []*plugnmeet.NatsKvUserInfo{{UserId: "b"}, {UserId: "a"}}
	if got := pickDelegate(humans, nil); got.GetUserId() != "b" {
		t.Errorf("delegate = %q, want the first online human", got.GetUserId())
	}

	withAdmin := []*plugnmeet.NatsKvUserInfo{{UserId: "b"}, {UserId: "a", IsAdmin: true}}
	if got := pickDelegate(withAdmin, nil); got.GetUserId() != "a" {
		t.Errorf("delegate = %q, want the admin", got.GetUserId())
	}
}

func TestSanitizeWhiteboardSceneDropsOversizedAndNonWebPFrames(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	sceneGraph := `{"elements":[]}`

	valid := []byte("RIFF\x00\x00\x00\x00WEBP")

	oversized := make([]byte, aiAgentMaxWhiteboardImageBytes+1)
	copy(oversized, valid)
	msg := &plugnmeet.AIAgentWhiteboardSceneMsg{WebpImage: oversized, SceneGraphJson: sceneGraph}
	if !m.sanitizeWhiteboardScene("room-1", msg) {
		t.Fatal("the scene graph must survive a dropped image")
	}
	if len(msg.GetWebpImage()) != 0 {
		t.Errorf("oversized image kept: %d bytes", len(msg.GetWebpImage()))
	}

	// A PNG frame or garbage never reaches the provider as webp.
	msg = &plugnmeet.AIAgentWhiteboardSceneMsg{WebpImage: []byte("\x89PNG\r\n\x1a\n"), SceneGraphJson: sceneGraph}
	m.sanitizeWhiteboardScene("room-1", msg)
	if len(msg.GetWebpImage()) != 0 {
		t.Error("a non-webp image must be dropped")
	}

	msg = &plugnmeet.AIAgentWhiteboardSceneMsg{WebpImage: append([]byte(nil), valid...)}
	if !m.sanitizeWhiteboardScene("room-1", msg) {
		t.Fatal("a valid webp frame must be delivered")
	}
	if len(msg.GetWebpImage()) != len(valid) {
		t.Errorf("valid image = %d bytes, want %d", len(msg.GetWebpImage()), len(valid))
	}

	// An oversized scene graph drops the whole snapshot.
	msg = &plugnmeet.AIAgentWhiteboardSceneMsg{
		WebpImage:      append([]byte(nil), valid...),
		SceneGraphJson: strings.Repeat("x", aiAgentMaxWhiteboardSceneBytes+1),
	}
	if m.sanitizeWhiteboardScene("room-1", msg) {
		t.Error("an oversized scene graph must drop the snapshot")
	}
}

func TestClampAIAgentChatTextCapsTheParticipantMessage(t *testing.T) {
	if got := clampAIAgentChatText("   how are you?  "); got != "how are you?" {
		t.Errorf("text = %q, want the trimmed message", got)
	}

	long := strings.Repeat("é", aiAgentSendContextMaxRunes+500)
	if got := []rune(clampAIAgentChatText(long)); len(got) != aiAgentSendContextMaxRunes {
		t.Errorf("runes = %d, want the cap of %d", len(got), aiAgentSendContextMaxRunes)
	}

	if got := clampAIAgentChatText("   "); got != "" {
		t.Errorf("text = %q, want an empty message", got)
	}
}
