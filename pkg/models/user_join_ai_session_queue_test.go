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
	"google.golang.org/protobuf/encoding/protojson"

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

	ctx := memoryJetStreamContext(t)
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
	rs := redisservice.New(redisservice.Args{Ctx: ctx, Rc: newAIAgentTestRedis(t), App: app, Logger: logger})
	breakoutModel := &BreakoutRoomModel{
		rs:          rs,
		natsService: natsService,
		rm:          &RoomModel{rs: rs, natsService: natsService, logger: logger.WithField("model", "room")},
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

func (e *aiSessionTestEnv) seedParentRoom(t *testing.T, meta *plugnmeet.RoomMetadata) {
	t.Helper()

	if _, err := e.nats.AddRoom(1, aiSessionTestParentId, "room-sid", nil, nil, meta); err != nil {
		t.Fatalf("AddRoom: %v", err)
	}
	if err := e.nats.UpdateRoomStatus(aiSessionTestParentId, natsservice.RoomStatusActive); err != nil {
		t.Fatalf("UpdateRoomStatus: %v", err)
	}
}

// seedSessionRoom registers an open AI session room created `ageSec` seconds ago.
func (e *aiSessionTestEnv) seedSessionRoom(t *testing.T, userId string, ageSec, durationMinutes uint64) {
	t.Helper()

	roomId := perUserAIBreakoutRoomId(aiSessionTestParentId, userId)
	raw, err := protojson.Marshal(&plugnmeet.BreakoutRoom{
		Id:       roomId,
		Created:  uint64(time.Now().Unix()) - ageSec,
		Duration: durationMinutes,
	})
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	if err = e.breakoutModel.rs.InsertOrUpdateBreakoutRoom(aiSessionTestParentId, roomId, raw); err != nil {
		t.Fatalf("InsertOrUpdateBreakoutRoom: %v", err)
	}
}

// queueInfo returns the queue info stored in the metadata of a waiting participant.
func (e *aiSessionTestEnv) queueInfo(t *testing.T, userId string) *plugnmeet.WaitingQueueInfo {
	t.Helper()

	info, err := e.nats.GetUserInfo(aiSessionTestParentId, userId)
	if err != nil || info == nil {
		t.Fatalf("GetUserInfo(%s): info=%v err=%v", userId, info, err)
	}
	md, err := e.nats.UnmarshalUserMetadata(info.GetMetadata())
	if err != nil {
		t.Fatalf("UnmarshalUserMetadata(%s): %v", userId, err)
	}
	return md.GetWaitingQueueInfo()
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

// queueTurnAtTolerance allows for the seconds passing between seeding and computing.
const queueTurnAtTolerance = 5000

func assertQueueInfo(t *testing.T, got *plugnmeet.WaitingQueueInfo, pos, total uint32, waitSec uint64) {
	t.Helper()

	if got == nil {
		t.Fatalf("waiting queue info is nil, want position %d", pos)
	}
	if got.GetPosition() != pos {
		t.Errorf("position = %d, want %d", got.GetPosition(), pos)
	}
	if got.GetTotalWaiting() != total {
		t.Errorf("total waiting = %d, want %d", got.GetTotalWaiting(), total)
	}
	if got.GetEstimatedWaitSeconds() != waitSec {
		t.Errorf("estimated wait seconds = %d, want %d", got.GetEstimatedWaitSeconds(), waitSec)
	}
	wantTurnAt := (time.Now().Unix() + int64(waitSec)) * 1000
	if diff := got.GetEstimatedTurnAt() - wantTurnAt; diff < -queueTurnAtTolerance || diff > queueTurnAtTolerance {
		t.Errorf("estimated turn at = %d, want ~%d", got.GetEstimatedTurnAt(), wantTurnAt)
	}
}

func TestWaitingQueuePositionWithoutActiveSessions(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2))))
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-2", "Ben", 20, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-3", "Cid", 30, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	// two free slots and the default 1800s session length
	assertQueueInfo(t, env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-1"), 1, 3, 1800)
	assertQueueInfo(t, env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-2"), 2, 3, 1800)
	assertQueueInfo(t, env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-3"), 3, 3, 3600)
}

func TestWaitingQueueUsesRunningSessions(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2))))
	// 800s and 1700s are left of the default 1800s sessions
	env.seedSessionRoom(t, "active-1", 1000, 0)
	env.seedSessionRoom(t, "active-2", 100, 0)
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-2", "Ben", 20, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-3", "Cid", 30, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	first := env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-1")
	if first == nil || first.GetEstimatedWaitSeconds() < 790 || first.GetEstimatedWaitSeconds() > 800 {
		t.Fatalf("first wait = %v, want the shortest running session", first)
	}
	second := env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-2")
	if second == nil || second.GetEstimatedWaitSeconds() < 1690 || second.GetEstimatedWaitSeconds() > 1700 {
		t.Fatalf("second wait = %v, want the second running session", second)
	}
	third := env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-3")
	if third == nil || third.GetEstimatedWaitSeconds() < 2590 || third.GetEstimatedWaitSeconds() > 2600 {
		t.Fatalf("third wait = %v, want one full session after the shortest slot", third)
	}
}

func TestWaitingQueueClampsExpiredSession(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(1))))
	env.seedSessionRoom(t, "active-1", 5000, 0)
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-2", "Ben", 20, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	assertQueueInfo(t, env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-1"), 1, 2, 30)
	assertQueueInfo(t, env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-2"), 2, 2, 1830)
}

func TestSyncWaitingAISessionQueuePersistsPositions(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, agentRoomMetadata(agentFeatures(true, modeOpenPerUse, openPerUserConfig(2))))
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-2", "Ben", 20, false, &plugnmeet.UserMetadata{WaitForApproval: true})
	env.addOnlineUser(t, "learner-3", "Cid", 30, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	env.breakoutModel.syncWaitingAISessionQueue(aiSessionTestParentId)

	for i, userId := range []string{"learner-1", "learner-2", "learner-3"} {
		waitSec := uint64(1800)
		if i == 2 {
			waitSec = 3600
		}
		assertQueueInfo(t, env.queueInfo(t, userId), uint32(i+1), 3, waitSec)
	}

	// nothing changed in the queue, so the stored ETA must not be rewritten
	before := env.queueInfo(t, "learner-2")
	env.breakoutModel.syncWaitingAISessionQueue(aiSessionTestParentId)
	after := env.queueInfo(t, "learner-2")
	if before.GetEstimatedTurnAt() != after.GetEstimatedTurnAt() {
		t.Errorf("estimated turn at changed from %d to %d without a queue change", before.GetEstimatedTurnAt(), after.GetEstimatedTurnAt())
	}
}

func TestWaitingQueueIgnoresNormalRooms(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, &plugnmeet.RoomMetadata{RoomTitle: "regular"})
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{WaitForApproval: true})

	if info := env.breakoutModel.WaitingAISessionQueueInfo(aiSessionTestParentId, "learner-1"); info != nil {
		t.Fatalf("waiting queue info = %v, want nil outside an open per-user AI session", info)
	}
	env.breakoutModel.syncWaitingAISessionQueue(aiSessionTestParentId)
	if info := env.queueInfo(t, "learner-1"); info != nil {
		t.Fatalf("stored queue info = %v, want nil", info)
	}
}

func TestAdmittedUserMetadataClearsQueueInfo(t *testing.T) {
	src := &plugnmeet.UserMetadata{
		WaitForApproval:        true,
		IsPresenter:            true,
		WaitingQueueInfo:       &plugnmeet.WaitingQueueInfo{Position: 2, TotalWaiting: 4, EstimatedWaitSeconds: 1800},
		PreviousSessionContext: new("Learner: fractions"),
	}

	got := admittedUserMetadata(src)
	if got.GetWaitForApproval() {
		t.Error("wait_for_approval = true, want the participant admitted")
	}
	if got.GetIsPresenter() {
		t.Error("is_presenter = true, want a plain participant")
	}
	if got.GetWaitingQueueInfo() != nil {
		t.Errorf("waiting queue info = %v, want it cleared once the participant is admitted", got.GetWaitingQueueInfo())
	}
	if got.GetPreviousSessionContext() != "Learner: fractions" {
		t.Error("previous session context was lost")
	}
	if src.GetWaitingQueueInfo() == nil || !src.GetWaitForApproval() {
		t.Error("the source metadata must stay untouched")
	}
}

func TestApproveWaitingUserClearsQueueInfo(t *testing.T) {
	env := newAISessionTestEnv(t)
	env.seedParentRoom(t, &plugnmeet.RoomMetadata{RoomTitle: "regular"})
	env.addOnlineUser(t, "learner-1", "Ana", 10, false, &plugnmeet.UserMetadata{
		WaitForApproval:  true,
		WaitingQueueInfo: &plugnmeet.WaitingQueueInfo{Position: 1, TotalWaiting: 1},
	})

	info, err := env.nats.GetUserInfo(aiSessionTestParentId, "learner-1")
	if err != nil || info == nil {
		t.Fatalf("GetUserInfo: info=%v err=%v", info, err)
	}

	roomModel := &RoomModel{natsService: env.nats, logger: aiSessionTestLog()}
	if err = roomModel.approveUser(aiSessionTestParentId, "learner-1", info.GetMetadata()); err != nil {
		t.Fatalf("approveUser() error = %v", err)
	}

	approved, err := env.nats.GetUserInfo(aiSessionTestParentId, "learner-1")
	if err != nil || approved == nil {
		t.Fatalf("GetUserInfo: info=%v err=%v", approved, err)
	}
	md, err := env.nats.UnmarshalUserMetadata(approved.GetMetadata())
	if err != nil {
		t.Fatalf("UnmarshalUserMetadata: %v", err)
	}
	if md.GetWaitForApproval() {
		t.Error("wait_for_approval = true, want the participant approved")
	}
	if md.GetWaitingQueueInfo() != nil {
		t.Errorf("waiting queue info = %v, want it cleared after approval", md.GetWaitingQueueInfo())
	}
}
