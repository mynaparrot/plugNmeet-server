package models

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func activeRoomWithParticipants(roomId string, identities ...string) *plugnmeet.ActiveRoomWithParticipant {
	room := &plugnmeet.ActiveRoomWithParticipant{RoomInfo: &plugnmeet.ActiveRoomInfo{RoomId: roomId}}
	for _, identity := range identities {
		room.ParticipantsInfo = append(room.ParticipantsInfo, &livekit.ParticipantInfo{Identity: identity})
	}
	return room
}

func TestCountJoinedHumanParticipants(t *testing.T) {
	parent := activeRoomWithParticipants("parent", "learner-1", "learner-2")
	childA := activeRoomWithParticipants("parent-1", "learner-1")
	childB := activeRoomWithParticipants("parent-2", "learner-3")

	if got := countJoinedHumanParticipants(roomWithBreakouts(parent)...); got != 2 {
		t.Fatalf("count without breakouts = %d, want the 2 participants of the parent room", got)
	}

	parent.BreakoutRooms = []*plugnmeet.ActiveRoomWithParticipant{childA, childB}
	if got := countJoinedHumanParticipants(roomWithBreakouts(parent)...); got != 3 {
		t.Fatalf("count across parent and breakouts = %d, want 3 deduplicated learners", got)
	}

	if got := countJoinedHumanParticipants(); got != 0 {
		t.Fatalf("count without rooms = %d, want 0", got)
	}
	if got := countJoinedHumanParticipants(activeRoomWithParticipants("empty")); got != 0 {
		t.Fatalf("count of an empty room = %d, want 0", got)
	}
}

func TestCountJoinedHumanParticipantsSkipsInternalUsers(t *testing.T) {
	room := activeRoomWithParticipants("parent", "learner-1", config.AgentUserUserIdPrefix+"bot", "", "learner-1")
	if got := countJoinedHumanParticipants(room); got != 1 {
		t.Fatalf("count = %d, want only the human learner", got)
	}
}

func TestRoomWithBreakoutsReturnsRoomFirst(t *testing.T) {
	parent := activeRoomWithParticipants("parent")
	child := activeRoomWithParticipants("parent-1")
	parent.BreakoutRooms = []*plugnmeet.ActiveRoomWithParticipant{child}

	rooms := roomWithBreakouts(parent)
	if len(rooms) != 2 || rooms[0] != parent || rooms[1] != child {
		t.Fatalf("rooms = %v, want the parent followed by its breakout room", rooms)
	}
}

func TestNewActiveRoomInfo(t *testing.T) {
	row := &dbmodels.RoomInfo{
		RoomTitle:          "Room",
		RoomId:             "parent-room-1",
		Sid:                "sid-1",
		JoinedParticipants: 4,
		IsRunning:          1,
		IsBreakoutRoom:     1,
		ParentRoomID:       helpers.FormatBreakoutParentRoomRef("parent-room", 7),
		CreationTime:       1700000000,
	}

	info := newActiveRoomInfo(row, &plugnmeet.NatsKvRoomInfo{Metadata: "meta"})
	if info.GetMetadata() != "meta" {
		t.Errorf("metadata = %q, want the NATS metadata", info.GetMetadata())
	}
	if info.GetParentRoomId() != "parent-room" {
		t.Errorf("parent room id = %q, want the plain parent room id", info.GetParentRoomId())
	}
	if info.GetJoinedParticipants() != 4 || info.GetIsBreakoutRoom() != 1 || info.GetIsRunning() != 1 {
		t.Errorf("info = %+v, want the DB values carried over", info)
	}
	if info.GetRoomId() != row.RoomId || info.GetSid() != row.Sid || info.GetRoomTitle() != row.RoomTitle {
		t.Errorf("info = %+v, want the DB identity of the room", info)
	}
}

// newRoomInfoTestDb opens an in-memory sqlite database with the real room_info
// schema from sql_dump/install.sql, so DB queries are exercised as written.
func newRoomInfoTestDb(t *testing.T) (*dbservice.DatabaseService, *gorm.DB) {
	t.Helper()

	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	createTestSchemaFromInstallDump(t, gdb, "room_info")

	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	return dbservice.New(dbservice.Args{Ctx: ctx, Db: gdb, Logger: logger}), gdb
}

func TestGetActiveBreakoutRoomsByParentRef(t *testing.T) {
	ctx := context.Background()
	ds, gdb := newRoomInfoTestDb(t)

	parent := &dbmodels.RoomInfo{RoomId: "parent-room", Sid: "sid-parent", IsRunning: 1}
	if err := gdb.Create(parent).Error; err != nil {
		t.Fatalf("failed to create the parent row: %v", err)
	}
	parentRef := helpers.FormatBreakoutParentRoomRef(parent.RoomId, parent.ID)
	otherParentRef := helpers.FormatBreakoutParentRoomRef(parent.RoomId, parent.ID+1)

	rows := []dbmodels.RoomInfo{
		// running breakouts of this parent table id
		{RoomId: "parent-room-1", Sid: "sid-1", IsRunning: 1, IsBreakoutRoom: 1, ParentRoomID: parentRef},
		{RoomId: "parent-room-2", Sid: "sid-2", IsRunning: 1, IsBreakoutRoom: 1, ParentRoomID: parentRef},
		// same room id but another parent table id, an ended child and a main room
		{RoomId: "parent-room-3", Sid: "sid-3", IsRunning: 1, IsBreakoutRoom: 1, ParentRoomID: otherParentRef},
		{RoomId: "parent-room-4", Sid: "sid-4", IsRunning: 0, IsBreakoutRoom: 1, ParentRoomID: parentRef},
		{RoomId: "main-room", Sid: "sid-5", IsRunning: 1},
	}
	for i := range rows {
		if err := gdb.Create(&rows[i]).Error; err != nil {
			t.Fatalf("failed to create room row %q: %v", rows[i].RoomId, err)
		}
	}

	got, err := ds.GetActiveBreakoutRoomsByParentRef(ctx, parentRef)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	var roomIds []string
	for _, room := range got {
		if room.ParentRoomID != parentRef || room.IsRunning != 1 || room.IsBreakoutRoom != 1 {
			t.Fatalf("row %q does not belong to %q: %+v", room.RoomId, parentRef, room)
		}
		roomIds = append(roomIds, room.RoomId)
	}
	sort.Strings(roomIds)
	if want := []string{"parent-room-1", "parent-room-2"}; len(roomIds) != len(want) || roomIds[0] != want[0] || roomIds[1] != want[1] {
		t.Fatalf("room ids = %v, want %v", roomIds, want)
	}

	// an empty reference must not degrade into "all running breakout rooms"
	if rooms, err := ds.GetActiveBreakoutRoomsByParentRef(ctx, ""); err != nil || rooms != nil {
		t.Fatalf("empty reference = (%v, %v), want (nil, nil)", rooms, err)
	}
}
