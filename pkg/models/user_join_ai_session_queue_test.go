package models

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const aiSessionTestParentId = "room-1"

// aiSessionTestEnv wires the join flow onto an in-memory NATS bucket and an
// in-memory Redis.
type aiSessionTestEnv struct {
	nats          *natsservice.NatsService
	userModel     *UserModel
	breakoutModel *BreakoutRoomModel
}

func newAISessionTestEnv(t *testing.T) *aiSessionTestEnv {
	t.Helper()

	ctx := context.Background()
	ttl := 2 * time.Hour
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	app := &config.AppConfig{
		RedisInfo: connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:  config.NatsInfo{DefaultTTL: &ttl},
	}

	natsService := natsservice.New(natsservice.Args{
		Ctx:    ctx,
		App:    app,
		Js:     &memoryJetStream{entries: map[string][]byte{}},
		Logger: logger,
	})
	breakoutModel := &BreakoutRoomModel{
		rs:          redisservice.New(redisservice.Args{Ctx: ctx, Rc: newAIAgentTestRedis(t), App: app, Logger: logger}),
		natsService: natsService,
		logger:      logger.WithField("model", "breakout_room"),
	}
	userModel := &UserModel{
		natsService:   natsService,
		breakoutModel: breakoutModel,
		logger:        logger.WithField("model", "user"),
	}
	breakoutModel.um = userModel

	return &aiSessionTestEnv{nats: natsService, userModel: userModel, breakoutModel: breakoutModel}
}

// addOnlineUser joins a participant to the parent room with an explicit join
// time, so the FIFO order is deterministic.
func (e *aiSessionTestEnv) addOnlineUser(t *testing.T, userId, name string, joinedAt uint64, isAdmin bool, md *plugnmeet.UserMetadata) {
	t.Helper()

	if md == nil {
		md = new(plugnmeet.UserMetadata)
	}
	if err := e.nats.AddUser(aiSessionTestParentId, userId, name, isAdmin, false, md, plugnmeet.ClientType_WEB); err != nil {
		t.Fatalf("AddUser(%s): %v", userId, err)
	}
	if err := e.nats.UpdateUserStatus(aiSessionTestParentId, userId, natsservice.UserStatusOnline); err != nil {
		t.Fatalf("UpdateUserStatus(%s): %v", userId, err)
	}
	if err := e.nats.UpdateUserKeyValue(aiSessionTestParentId, userId, natsservice.UserJoinedAt, strconv.FormatUint(joinedAt, 10)); err != nil {
		t.Fatalf("joined_at(%s): %v", userId, err)
	}
}

func (e *aiSessionTestEnv) seedChildRoom(t *testing.T, userId string) string {
	t.Helper()

	childRoomId := perUserAIBreakoutRoomId(aiSessionTestParentId, userId)
	if err := e.breakoutModel.rs.InsertOrUpdateBreakoutRoom(aiSessionTestParentId, childRoomId, []byte(`{}`)); err != nil {
		t.Fatalf("failed to seed child room %s: %v", childRoomId, err)
	}
	return childRoomId
}

func newAISessionJoinRequest(userId string) *plugnmeet.GenerateTokenReq {
	return &plugnmeet.GenerateTokenReq{
		RoomId: aiSessionTestParentId,
		UserInfo: &plugnmeet.UserInfo{
			UserId: userId,
			Name:   "Ana",
			UserMetadata: &plugnmeet.UserMetadata{
				PreviousSessionContext: new("Learner: fractions"),
			},
		},
	}
}

func aiSessionTestLog() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	return logrus.NewEntry(logger)
}

func TestOpenPerUserAISessionDirectJoin(t *testing.T) {
	env := newAISessionTestEnv(t)
	meta := agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)))
	req := newAISessionJoinRequest("learner-1")

	ensure := func(_ context.Context, parentRoomId, userId, _ string, previousSessionContext string) (string, error) {
		if parentRoomId != aiSessionTestParentId {
			t.Errorf("parent room = %q, want %q", parentRoomId, aiSessionTestParentId)
		}
		if previousSessionContext != "Learner: fractions" {
			t.Errorf("previous session context = %q, want it carried into the session room", previousSessionContext)
		}
		return perUserAIBreakoutRoomId(parentRoomId, userId), nil
	}

	roomId, err := env.userModel.perUserAISessionRoom(context.Background(), req, meta, ensure, aiSessionTestLog())
	if err != nil {
		t.Fatalf("perUserAISessionRoom() error = %v, want nil", err)
	}
	if want := aiSessionTestParentId + "-learner-1"; roomId != want {
		t.Fatalf("perUserAISessionRoom() = %q, want %q", roomId, want)
	}
	if req.GetUserInfo().GetUserMetadata().GetWaitForApproval() {
		t.Error("wait_for_approval = true, want false")
	}

	// the child room looks the participant up in the parent room
	info, err := env.nats.GetUserInfo(aiSessionTestParentId, "learner-1")
	if err != nil || info == nil || info.GetUserId() != "learner-1" {
		t.Fatalf("user was not registered in the parent room: info=%v err=%v", info, err)
	}
}

func TestOpenPerUserAISessionQueuesWhenSessionsFull(t *testing.T) {
	env := newAISessionTestEnv(t)
	meta := agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(1)))
	req := newAISessionJoinRequest("learner-1")

	ensure := func(context.Context, string, string, string, string) (string, error) {
		return "", config.ErrAIAgentSessionsFull
	}

	roomId, err := env.userModel.perUserAISessionRoom(context.Background(), req, meta, ensure, aiSessionTestLog())
	if err != nil {
		t.Fatalf("perUserAISessionRoom() error = %v, want nil", err)
	}
	if roomId != "" {
		t.Fatalf("perUserAISessionRoom() = %q, want the parent room", roomId)
	}

	md := req.GetUserInfo().GetUserMetadata()
	if !md.GetWaitForApproval() {
		t.Error("wait_for_approval = false, want the participant queued")
	}
	if md.GetPreviousSessionContext() != "Learner: fractions" {
		t.Errorf("previous session context = %q, want it preserved while queueing", md.GetPreviousSessionContext())
	}
}

func TestControlledUsersSessionStillFailsWhenFull(t *testing.T) {
	env := newAISessionTestEnv(t)
	meta := agentRoomMetadata(agentFeatures(true, modeControlled, controlledConfig("learner-1")))
	req := newAISessionJoinRequest("learner-1")

	ensure := func(context.Context, string, string, string, string) (string, error) {
		return "", config.ErrAIAgentSessionsFull
	}

	_, err := env.userModel.perUserAISessionRoom(context.Background(), req, meta, ensure, aiSessionTestLog())
	if !errors.Is(err, config.ErrAIAgentSessionsFull) {
		t.Fatalf("perUserAISessionRoom() error = %v, want %v", err, config.ErrAIAgentSessionsFull)
	}
	if req.GetUserInfo().GetUserMetadata().GetWaitForApproval() {
		t.Error("wait_for_approval = true, want a controlled session to reject instead of queueing")
	}
}

func TestOpenPerUserAISessionQueuesBehindWaitingUsers(t *testing.T) {
	env := newAISessionTestEnv(t)
	meta := agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2)))
	req := newAISessionJoinRequest("learner-1")
	env.addOnlineUser(t, "learner-2", "Ben", 1, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	ensure := func(context.Context, string, string, string, string) (string, error) {
		t.Error("a free slot must not be handed out while another participant is waiting")
		return "", nil
	}

	roomId, err := env.userModel.perUserAISessionRoom(context.Background(), req, meta, ensure, aiSessionTestLog())
	if err != nil {
		t.Fatalf("perUserAISessionRoom() error = %v, want nil", err)
	}
	if roomId != "" {
		t.Fatalf("perUserAISessionRoom() = %q, want the parent room", roomId)
	}
	if !req.GetUserInfo().GetUserMetadata().GetWaitForApproval() {
		t.Error("wait_for_approval = false, want the participant queued behind the waiting users")
	}
}

func TestOpenPerUserAISessionRejoinWinsOverQueue(t *testing.T) {
	env := newAISessionTestEnv(t)
	meta := agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(1)))
	req := newAISessionJoinRequest("learner-1")
	childRoomId := env.seedChildRoom(t, "learner-1")
	env.addOnlineUser(t, "learner-2", "Ben", 1, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	ensure := func(context.Context, string, string, string, string) (string, error) {
		return childRoomId, nil
	}

	roomId, err := env.userModel.perUserAISessionRoom(context.Background(), req, meta, ensure, aiSessionTestLog())
	if err != nil {
		t.Fatalf("perUserAISessionRoom() error = %v, want nil", err)
	}
	if roomId != childRoomId {
		t.Fatalf("perUserAISessionRoom() = %q, want the existing room %q", roomId, childRoomId)
	}
	if req.GetUserInfo().GetUserMetadata().GetWaitForApproval() {
		t.Error("wait_for_approval = true, want an existing room to be reused at once")
	}
}

// TestWaitingAISessionUsersFIFOOrder locks in the order TryAdmitWaitingAISessionUsers
// admits the queued participants of an open per-user AI session.
func TestWaitingAISessionUsersFIFOOrder(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.addOnlineUser(t, "learner-late", "Late", 300, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-first", "First", 100, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-middle", "Middle", 200, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-active", "Active", 50, false, nil)
	env.addOnlineUser(t, "admin-1", "Admin", 10, true, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, config.AgentUserUserIdPrefix+"ai_agent-1", "Agent", 20, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	waiting := env.breakoutModel.waitingAISessionUsers(aiSessionTestParentId, "")
	want := []string{"learner-first", "learner-middle", "learner-late"}
	if len(waiting) != len(want) {
		t.Fatalf("got %d waiting users, want %d", len(waiting), len(want))
	}
	for i, id := range want {
		if waiting[i].user.GetUserId() != id {
			t.Fatalf("waiting user %d = %q, want %q", i, waiting[i].user.GetUserId(), id)
		}
	}

	if !env.breakoutModel.hasWaitingAISessionUsers(aiSessionTestParentId, "learner-active") {
		t.Error("hasWaitingAISessionUsers() = false, want true")
	}
	if got := env.breakoutModel.waitingAISessionUsers(aiSessionTestParentId, "learner-first"); len(got) != 2 {
		t.Errorf("got %d waiting users without the excluded one, want 2", len(got))
	}
	if env.breakoutModel.HasPerUserAIBreakoutRoom(aiSessionTestParentId, "learner-first") {
		t.Error("HasPerUserAIBreakoutRoom() = true, want false for a participant without a room")
	}

	childRoomId := env.seedChildRoom(t, "learner-first")
	if !env.breakoutModel.HasPerUserAIBreakoutRoom(aiSessionTestParentId, "learner-first") {
		t.Fatalf("HasPerUserAIBreakoutRoom() = false, want true for %s", childRoomId)
	}
}
