package models

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

// A 1-on-1 room names its participants: an unlisted user may never be picked,
// not even an admin who only joins to observe.
func TestPickDelegateKeepsUnauthorizedUsersOutOfAOneOnOneRoom(t *testing.T) {
	allowed := []string{"learner-1"}

	onlyStranger := []*plugnmeet.NatsKvUserInfo{{UserId: "stranger"}}
	if got := pickDelegate(onlyStranger, allowed); got != nil {
		t.Fatalf("delegate = %q, want none", got.GetUserId())
	}

	observers := []*plugnmeet.NatsKvUserInfo{
		{UserId: "stranger"},
		{UserId: "teacher", IsAdmin: true},
	}
	if got := pickDelegate(observers, allowed); got != nil {
		t.Fatalf("delegate = %q, want none without the named participant", got.GetUserId())
	}

	withLearner := append([]*plugnmeet.NatsKvUserInfo{{UserId: "learner-1"}}, observers...)
	if got := pickDelegate(withLearner, allowed); got.GetUserId() != "learner-1" {
		t.Errorf("delegate = %q, want the named participant", got.GetUserId())
	}
}

// newDelegateRoomNatsService builds a NATS service whose bucket holds the given
// 1-on-1 room and its online users.
func newDelegateRoomNatsService(t *testing.T, meta *plugnmeet.RoomMetadata, users ...*plugnmeet.NatsKvUserInfo) *natsservice.NatsService {
	t.Helper()

	rawMeta, err := protojson.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal room metadata: %v", err)
	}
	entries := map[string][]byte{natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey: rawMeta}
	for _, user := range users {
		prefix := natsservice.UserKeyPrefix + user.GetUserId() + natsservice.UserKeyFieldPrefix
		entries[prefix+natsservice.UserStatusKey] = []byte(natsservice.UserStatusOnline)
		entries[prefix+natsservice.UserIsAdminKey] = []byte(strconv.FormatBool(user.GetIsAdmin()))
	}
	ttl := 2 * time.Hour

	return natsservice.New(natsservice.Args{
		Ctx:    memoryJetStreamContext(t),
		App:    &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}},
		Js:     &memoryJetStream{entries: entries},
		Logger: logrus.New(),
	})
}

// TestIsEligibleDelegateKeepsAnObservingAdminOutOfAOneOnOneRoom locks in that an
// admin who is not one of the named participants may watch the session but never
// drives it.
func TestIsEligibleDelegateKeepsAnObservingAdminOutOfAOneOnOneRoom(t *testing.T) {
	room := agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("learner-1")))
	room.IsBreakoutRoom = true

	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	model.natsService = newDelegateRoomNatsService(t, room,
		&plugnmeet.NatsKvUserInfo{UserId: "learner-1"},
		&plugnmeet.NatsKvUserInfo{UserId: "admin-1", IsAdmin: true},
	)

	if !model.isEligibleDelegate("room-1", "learner-1") {
		t.Error("the named participant must be eligible")
	}
	if model.isEligibleDelegate("room-1", "admin-1") {
		t.Error("an observing admin must not be eligible")
	}

	groupRoom := newAIAgentTestModel(newAIAgentTestRedis(t))
	groupRoom.natsService = newDelegateRoomNatsService(t, agentRoomMetadata(agentFeatures(true, modeNormalRoom, nil)),
		&plugnmeet.NatsKvUserInfo{UserId: "admin-1", IsAdmin: true},
	)
	if !groupRoom.isEligibleDelegate("room-1", "admin-1") {
		t.Error("a room without a roster may delegate to its admin")
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

// TestCallClientToolForUserRejectsADuplicateCallID locks in that one call id can
// only ever be in flight once, so a retried call cannot answer itself.
func TestCallClientToolForUserRejectsADuplicateCallID(t *testing.T) {
	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan clientCallOutcome, 1)
	go func() {
		res, err := model.CallClientToolForUser(ctx, "admin-1", mcp.ToolCall{
			CallID:   "call-1",
			RoomID:   "room-1",
			ToolName: mcp.ToolNotepadRead,
		}, time.Minute)
		done <- clientCallOutcome{res: res, err: err}
	}()

	waitForPendingClientCall(t, model)
	_, err := model.CallClientToolForUser(ctx, "admin-1", mcp.ToolCall{
		CallID:   "call-1",
		RoomID:   "room-1",
		ToolName: mcp.ToolNotepadRead,
	}, time.Second)
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("second call = %v, want the duplicate call id to be refused", err)
	}
	if got := pendingClientCallCount(model); got != 1 {
		t.Errorf("pending calls = %d, want the refused call to leave the first one in flight", got)
	}

	cancel()
	<-done
}

// TestDelegateClientRPCSenderNeedsAnOnlineDelegate locks in that the agent
// session reaches a browser through the room's delegate only.
func TestDelegateClientRPCSenderNeedsAnOnlineDelegate(t *testing.T) {
	model := newAIAgentTestModel(newAIAgentTestRedis(t))

	sender := model.NewDelegateClientRPCSender()
	if sender == nil {
		t.Fatal("the sender must be wired to the model")
	}
	if _, err := sender.CallClientTool(context.Background(), mcp.ToolCall{
		RoomID:   "room-1",
		ToolName: mcp.ToolNotepadRead,
	}, time.Second); err == nil {
		t.Fatal("a room without a delegate has no browser to run the tool")
	}
	if got := pendingClientCallCount(model); got != 0 {
		t.Errorf("pending calls = %d, want no call without a delegate", got)
	}
}

// TestBuildToolDependenciesDefaultsToTheDelegateSender locks in that a session
// built without a client RPC still gets the model's delegate broker.
func TestBuildToolDependenciesDefaultsToTheDelegateSender(t *testing.T) {
	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	log := logrus.NewEntry(logrus.New())

	if deps := model.BuildToolDependencies("room-1", "user-1", nil, log); deps.Client == nil {
		t.Error("a missing client RPC must default to the delegate sender")
	}

	targeted := model.NewUserClientRPCSender("admin-1")
	if deps := model.BuildToolDependencies("room-1", "user-1", targeted, log); deps.Client != targeted {
		t.Error("an explicit client RPC must be kept")
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
