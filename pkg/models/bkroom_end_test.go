package models

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const bkRoomEndTestRoomId = "room-1"

// newBkRoomEndTestNatsService returns a NATS service whose room bucket holds the
// info of bkRoomEndTestRoomId. The room features are already inactive, so no
// metadata update is needed to end its breakout rooms.
func newBkRoomEndTestNatsService(t *testing.T, createdAt, emptyTimeout uint64) (*natsservice.NatsService, *memoryJetStream) {
	t.Helper()

	js := &memoryJetStream{entries: map[string][]byte{
		natsservice.RoomInfoKeyPrefix + natsservice.RoomDbTableIdKey:    []byte("1"),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomIdKey:           []byte(bkRoomEndTestRoomId),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomSidKey:          []byte("RM_room_1"),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomStatusKey:       []byte(natsservice.RoomStatusActive),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomCreatedKey:      []byte(strconv.FormatUint(createdAt, 10)),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomEmptyTimeoutKey: []byte(strconv.FormatUint(emptyTimeout, 10)),
		natsservice.RoomInfoKeyPrefix + natsservice.RoomMetadataKey:     []byte(`{"room_title":"parent","room_features":{"breakout_room_features":{"is_active":false}}}`),
	}}

	ttl := 2 * time.Hour
	s := natsservice.New(natsservice.Args{
		Ctx:    context.Background(),
		App:    &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}},
		Js:     js,
		Logger: logrus.New(),
	})

	return s, js
}

// assertRefreshedEmptyTimeout verifies that the room expires around
// now+graceSeconds instead of createdAt+emptyTimeout.
func assertRefreshedEmptyTimeout(t *testing.T, js *memoryJetStream, createdAt, graceSeconds uint64) {
	t.Helper()

	raw, ok := js.entries[natsservice.RoomInfoKeyPrefix+natsservice.RoomEmptyTimeoutKey]
	if !ok {
		t.Fatal("empty timeout was not stored in the room bucket")
	}
	timeout, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil {
		t.Fatalf("empty timeout %q is not a number: %v", raw, err)
	}

	now := uint64(time.Now().UTC().Unix())
	expiresAt := createdAt + timeout
	want := now + graceSeconds
	if expiresAt < want-2 || expiresAt > want+2 {
		t.Fatalf("room expires at %d (timeout %d), want ~%d", expiresAt, timeout, want)
	}
}

func TestRefreshRoomEmptyTimeout(t *testing.T) {
	now := uint64(time.Now().UTC().Unix())
	tests := []struct {
		name      string
		createdAt uint64
	}{
		{name: "recently created room", createdAt: now - 60},
		{name: "room created more than 30 minutes ago", createdAt: now - 3600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, js := newBkRoomEndTestNatsService(t, tt.createdAt, 1800)

			if err := s.RefreshRoomEmptyTimeout(bkRoomEndTestRoomId, parentRoomPostBreakoutEmptyTimeout); err != nil {
				t.Fatalf("RefreshRoomEmptyTimeout: %v", err)
			}

			assertRefreshedEmptyTimeout(t, js, tt.createdAt, parentRoomPostBreakoutEmptyTimeout)
		})
	}
}

// newBkRoomEndTestModel returns a breakout room model whose parent room info is
// served from an in-memory NATS bucket and whose breakout rooms live in miniredis.
func newBkRoomEndTestModel(t *testing.T, createdAt, emptyTimeout uint64) (*BreakoutRoomModel, *memoryJetStream) {
	t.Helper()

	ctx := context.Background()
	ttl := 2 * time.Hour
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	app := &config.AppConfig{
		RedisInfo: connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:  config.NatsInfo{DefaultTTL: &ttl},
	}

	natsService, js := newBkRoomEndTestNatsService(t, createdAt, emptyTimeout)
	m := &BreakoutRoomModel{
		rs:          redisservice.New(redisservice.Args{Ctx: ctx, Rc: newAIAgentTestRedis(t), App: app, Logger: logger}),
		natsService: natsService,
		logger:      logger.WithField("model", "breakout_room"),
	}

	return m, js
}

func TestOnAfterBkRoomEndedRefreshesParentRoomEmptyTimeout(t *testing.T) {
	createdAt := uint64(time.Now().UTC().Unix()) - 1200

	tests := []struct {
		name string
		// remainingRooms are breakout rooms still stored in the parent's hash when
		// another child room ends, e.g. unused pre-created admin rooms.
		remainingRooms []string
	}{
		{name: "last breakout room ended"},
		{name: "unused pre-created room is left", remainingRooms: []string{"room01-admin"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, js := newBkRoomEndTestModel(t, createdAt, 1800)
			for _, id := range tt.remainingRooms {
				if err := m.rs.InsertOrUpdateBreakoutRoom(bkRoomEndTestRoomId, id, []byte("{}")); err != nil {
					t.Fatalf("failed to seed breakout room %s: %v", id, err)
				}
			}

			m.onAfterBkRoomEnded(bkRoomEndTestRoomId, bkRoomEndTestRoomId+"-1", m.logger.WithField("method", "test"))

			assertRefreshedEmptyTimeout(t, js, createdAt, parentRoomPostBreakoutEmptyTimeout)

			count, err := m.rs.CountBreakoutRooms(bkRoomEndTestRoomId)
			if err != nil {
				t.Fatalf("CountBreakoutRooms: %v", err)
			}
			if want := int64(len(tt.remainingRooms)); count != want {
				t.Fatalf("breakout room count is %d, want %d", count, want)
			}
		})
	}
}
