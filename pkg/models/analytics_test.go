package models

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
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
		RedisInfo: connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:  config.NatsInfo{DefaultTTL: &ttl},
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
