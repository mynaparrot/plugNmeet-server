package models

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newTestAnalyticsModel(t *testing.T) (*AnalyticsModel, redis.UniversalClient) {
	t.Helper()

	host := os.Getenv("TEST_REDIS_HOST")
	if host == "" {
		host = "localhost:6379"
	}
	ctx := context.Background()

	rc := redis.NewClient(&redis.Options{Addr: host})
	if err := rc.Ping(ctx).Err(); err != nil {
		t.Skipf("no redis fixture at %s: %v", host, err)
	}
	t.Cleanup(func() {
		_ = rc.Close()
	})

	ttl := 2 * time.Minute
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	app := &config.AppConfig{
		AnalyticsSettings: &config.AnalyticsSettings{Enabled: true},
		RedisInfo:         connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:          config.NatsInfo{DefaultTTL: &ttl},
	}

	m := &AnalyticsModel{
		ctx:         ctx,
		app:         app,
		rs:          redisservice.New(redisservice.Args{Ctx: ctx, Rc: rc, App: app, Logger: logger}),
		natsService: natsservice.New(natsservice.Args{Ctx: ctx, App: app, Logger: logger}),
		logger:      logger.WithField("model", "analytics"),
	}

	return m, rc
}

func userJoinedMsg(roomId, userId, userName, extraData string) *plugnmeet.AnalyticsDataMsg {
	return &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED,
		RoomId:    roomId,
		Time:      time.Now().UnixMilli(),
		UserId:    &userId,
		UserName:  &userName,
		ExtraData: &extraData,
	}
}

func checkUserJoined(t *testing.T, m *AnalyticsModel, usersKey, eventKey, userId string, wantAdmin bool, wantExUserId string) {
	t.Helper()

	users, err := m.rs.AnalyticsGetAllUsers(usersKey)
	if err != nil {
		t.Fatalf("read analytics users: %v", err)
	}
	raw, ok := users[userId]
	if !ok {
		t.Fatalf("user %q not recorded: %v", userId, users)
	}

	info := new(plugnmeet.AnalyticsRedisUserInfo)
	if err := protojson.Unmarshal([]byte(raw), info); err != nil {
		t.Fatalf("unmarshal user info %s: %v", raw, err)
	}
	if info.GetName() != "User One" || info.GetIsAdmin() != wantAdmin || info.GetExUserId() != wantExUserId {
		t.Errorf("user info = %v, want name=User One is_admin=%v ex_user_id=%q", info, wantAdmin, wantExUserId)
	}

	events, err := m.rs.GetAnalyticsAllHashTypeVals(eventKey)
	if err != nil {
		t.Fatalf("read user joined events: %v", err)
	}
	if len(events) == 0 {
		t.Errorf("user_joined event not recorded for key %s", eventKey)
	}
}

func TestHandleFirstTimeUserJoinedMalformedExtraData(t *testing.T) {
	m, rc := newTestAnalyticsModel(t)
	ctx := context.Background()

	roomId := "room-analytics-malformed"
	userId := "user-1"
	key := fmt.Sprintf(analyticsRoomKey+":room", roomId)
	usersKey := key + ":users"
	eventKey := fmt.Sprintf(analyticsUserKey, roomId, userId) + ":" + plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED.String()
	t.Cleanup(func() {
		rc.Del(ctx, usersKey, eventKey)
	})

	// The parse error keeps the default metadata, so the join must still be recorded.
	m.handleFirstTimeUserJoined(userJoinedMsg(roomId, userId, "User One", "not-json"), key)

	checkUserJoined(t, m, usersKey, eventKey, userId, false, "")
}

func TestHandleFirstTimeUserJoinedValidExtraData(t *testing.T) {
	m, rc := newTestAnalyticsModel(t)
	ctx := context.Background()

	roomId := "room-analytics-valid"
	userId := "user-1"
	key := fmt.Sprintf(analyticsRoomKey+":room", roomId)
	usersKey := key + ":users"
	eventKey := fmt.Sprintf(analyticsUserKey, roomId, userId) + ":" + plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED.String()
	t.Cleanup(func() {
		rc.Del(ctx, usersKey, eventKey)
	})

	tests := []struct {
		name      string
		metadata  string
		wantAdmin bool
		wantEx    string
	}{
		{name: "empty object", metadata: "{}"},
		{name: "populated metadata", metadata: `{"is_admin":true,"ex_user_id":"ext-42"}`, wantAdmin: true, wantEx: "ext-42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc.Del(ctx, usersKey, eventKey)

			m.handleFirstTimeUserJoined(userJoinedMsg(roomId, userId, "User One", tt.metadata), key)

			checkUserJoined(t, m, usersKey, eventKey, userId, tt.wantAdmin, tt.wantEx)
		})
	}
}

// fakeAnalyticsNats lets the attribution logic be tested without NATS.
type fakeAnalyticsNats struct {
	meta *plugnmeet.RoomMetadata
	err  error
}

func (f fakeAnalyticsNats) GetRoomMetadataStruct(string) (*plugnmeet.RoomMetadata, error) {
	return f.meta, f.err
}

func (f fakeAnalyticsNats) UnmarshalUserMetadata(string) (*plugnmeet.UserMetadata, error) {
	return &plugnmeet.UserMetadata{}, nil
}

func (f fakeAnalyticsNats) UnmarshalRoomMetadata(string) (*plugnmeet.RoomMetadata, error) {
	if f.meta != nil {
		return f.meta, f.err
	}
	return &plugnmeet.RoomMetadata{}, f.err
}

func TestHandleEventIgnoresInternalAgentUsers(t *testing.T) {
	m, rc := newTestAnalyticsModel(t)
	ctx := context.Background()
	// Keep attribution local so this test does not need NATS.
	m.natsService = fakeAnalyticsNats{}

	roomId := "room-analytics-internal-agent"
	usersKey := fmt.Sprintf(analyticsRoomKey+":room", roomId) + ":users"
	t.Cleanup(func() {
		rc.Del(ctx, usersKey)
	})

	internalUserId := config.AgentUserUserIdPrefix + "ai_agent-test"
	m.HandleEvent(userJoinedMsg(roomId, internalUserId, "AI Agent", "{}"))

	users, err := m.rs.AnalyticsGetAllUsers(usersKey)
	if err != nil {
		t.Fatalf("read analytics users: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("internal agent user was recorded in analytics: %v", users)
	}

	// A real participant on the same path is still recorded.
	m.HandleEvent(userJoinedMsg(roomId, "user-1", "User One", "{}"))
	users, err = m.rs.AnalyticsGetAllUsers(usersKey)
	if err != nil {
		t.Fatalf("read analytics users: %v", err)
	}
	if _, ok := users["user-1"]; !ok {
		t.Fatalf("real participant missing from analytics users: %v", users)
	}
}

func TestAnalyticsRoomIdUsesBreakoutMetadata(t *testing.T) {
	parentRoomId := "parent-room"
	m := &AnalyticsModel{
		natsService: fakeAnalyticsNats{meta: &plugnmeet.RoomMetadata{
			IsBreakoutRoom: true,
			ParentRoomId:   helpers.FormatBreakoutParentRoomRef(parentRoomId, 42),
		}},
		logger: logrus.New().WithField("test", "analytics"),
	}

	if got := m.analyticsRoomId("breakout-room"); got != parentRoomId {
		t.Fatalf("analyticsRoomId = %q, want %q", got, parentRoomId)
	}

	// A main room is attributed to itself.
	m.natsService = fakeAnalyticsNats{meta: &plugnmeet.RoomMetadata{}}
	if got := m.analyticsRoomId("main-room"); got != "main-room" {
		t.Fatalf("analyticsRoomId = %q, want main-room", got)
	}
}

func TestAnalyticsRoomIdFallsBackToDb(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	createTestSchemaFromInstallDump(t, gdb, "room_info")
	ds := dbservice.New(dbservice.Args{Ctx: ctx, Db: gdb, Logger: logrus.New()})

	parent := createTestRoomRow(t, gdb, "parent-room", false, "")
	child := createTestRoomRow(t, gdb, "breakout-child", true, helpers.FormatBreakoutParentRoomRef(parent.RoomId, parent.ID))

	m := &AnalyticsModel{
		ds: ds,
		// No metadata: this is the state after the breakout room's NATS bucket
		// has already been removed.
		natsService: fakeAnalyticsNats{},
		logger:      logrus.New().WithField("test", "analytics"),
	}

	if got := m.analyticsRoomId(child.RoomId); got != parent.RoomId {
		t.Fatalf("analyticsRoomId = %q, want parent %q", got, parent.RoomId)
	}
	// Unknown rooms keep their own id.
	if got := m.analyticsRoomId("unknown-room"); got != "unknown-room" {
		t.Fatalf("analyticsRoomId = %q, want unknown-room", got)
	}
}
