package helpers

import (
	"context"
	"testing"

	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newWebhookNotifierTestDb(t *testing.T) (*dbservice.DatabaseService, *gorm.DB) {
	t.Helper()

	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatalf("failed to open sqlite test db: %v", err)
	}
	// only the columns used by the notifier lookups
	ddl := "CREATE TABLE room_info (id integer primary key autoincrement, roomId text, sid text, webhook_url text, is_breakout_room integer, parent_room_id text)"
	if err := gdb.Exec(ddl).Error; err != nil {
		t.Fatalf("failed to create room_info test table: %v", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)

	return dbservice.New(dbservice.Args{Ctx: context.Background(), Db: gdb, Logger: logger}), gdb
}

func TestSetBreakoutParentFields(t *testing.T) {
	room := &plugnmeet.NotifyEventRoom{Sid: new("sid-child"), RoomId: new("child-room")}
	setBreakoutParentFields(room, "parent-room", "sid-parent")

	if room.GetRoomId() != "child-room" || room.GetSid() != "sid-child" {
		t.Fatalf("room identity changed: room_id=%q sid=%q", room.GetRoomId(), room.GetSid())
	}
	if !room.GetIsBreakoutRoom() {
		t.Fatal("is_breakout_room should be set")
	}
	if room.GetParentRoomId() != "parent-room" {
		t.Fatalf("parent_room_id = %q, want parent-room", room.GetParentRoomId())
	}
	if room.GetParentRoomSid() != "sid-parent" {
		t.Fatalf("parent_room_sid = %q, want sid-parent", room.GetParentRoomSid())
	}

	// an unknown parent sid must stay unset instead of being sent as an empty value
	room = &plugnmeet.NotifyEventRoom{}
	setBreakoutParentFields(room, "parent-room", "")
	if room.ParentRoomSid != nil {
		t.Fatal("parent_room_sid should not be set without a parent sid")
	}
}

func TestResolveParentRoomInfo(t *testing.T) {
	ds, gdb := newWebhookNotifierTestDb(t)

	const parentTableId = 1
	if err := gdb.Exec("INSERT INTO room_info (id, roomId, sid, is_breakout_room, parent_room_id) VALUES (?, 'parent-room', 'sid-parent', 0, '')", parentTableId).Error; err != nil {
		t.Fatalf("failed to create the parent room row: %v", err)
	}

	w := &WebhookNotifier{ds: ds}

	tests := []struct {
		name             string
		roomInfo         *dbmodels.RoomInfo
		wantBreakout     bool
		wantParentRoomId string
		wantParentSid    string
	}{
		{
			name:     "nil room info",
			roomInfo: nil,
		},
		{
			name:     "main room",
			roomInfo: &dbmodels.RoomInfo{RoomId: "main-room", Sid: "sid-main"},
		},
		{
			name:     "breakout room with malformed parent ref",
			roomInfo: &dbmodels.RoomInfo{RoomId: "child", Sid: "sid-child", IsBreakoutRoom: 1, ParentRoomID: "parent-room"},
		},
		{
			name:             "breakout room with parent ref",
			roomInfo:         &dbmodels.RoomInfo{RoomId: "child", Sid: "sid-child", IsBreakoutRoom: 1, ParentRoomID: FormatBreakoutParentRoomRef("parent-room", parentTableId)},
			wantBreakout:     true,
			wantParentRoomId: "parent-room",
			wantParentSid:    "sid-parent",
		},
		{
			name:             "breakout room whose parent row belongs to another room",
			roomInfo:         &dbmodels.RoomInfo{RoomId: "child", Sid: "sid-child", IsBreakoutRoom: 1, ParentRoomID: FormatBreakoutParentRoomRef("other-room", parentTableId)},
			wantBreakout:     true,
			wantParentRoomId: "other-room",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isBreakoutRoom, parentRoomId, parentRoomSid := w.resolveParentRoomInfo(tt.roomInfo)
			if isBreakoutRoom != tt.wantBreakout {
				t.Fatalf("isBreakoutRoom = %v, want %v", isBreakoutRoom, tt.wantBreakout)
			}
			if parentRoomId != tt.wantParentRoomId {
				t.Fatalf("parentRoomId = %q, want %q", parentRoomId, tt.wantParentRoomId)
			}
			if parentRoomSid != tt.wantParentSid {
				t.Fatalf("parentRoomSid = %q, want %q", parentRoomSid, tt.wantParentSid)
			}
		})
	}

	// without a database, the parent room id must still be resolved
	w = &WebhookNotifier{}
	isBreakoutRoom, parentRoomId, parentRoomSid := w.resolveParentRoomInfo(
		&dbmodels.RoomInfo{IsBreakoutRoom: 1, ParentRoomID: FormatBreakoutParentRoomRef("parent-room", parentTableId)},
	)
	if !isBreakoutRoom || parentRoomId != "parent-room" || parentRoomSid != "" {
		t.Fatalf("without db = (%v, %q, %q)", isBreakoutRoom, parentRoomId, parentRoomSid)
	}
}

func TestWebhookRedisFieldsParentRoomPersistence(t *testing.T) {
	d := &webhookRedisFields{
		Urls:            []string{"https://example.com/hook"},
		PerformDeleting: false,
		IsBreakoutRoom:  true,
		ParentRoomId:    "parent-room",
		ParentRoomSid:   "sid-parent",
	}

	marshal, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("failed to marshal webhook fields: %v", err)
	}

	restored := new(webhookRedisFields)
	if err := json.Unmarshal(marshal, restored); err != nil {
		t.Fatalf("failed to unmarshal webhook fields: %v", err)
	}
	if !restored.IsBreakoutRoom || restored.ParentRoomId != "parent-room" || restored.ParentRoomSid != "sid-parent" {
		t.Fatalf("restored parent room fields = %+v", restored)
	}
}
