package controllers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// recorderResponseMsgKeyRe matches the i18n key shape the recorder task
// responses must carry: dot separated lower-case segments, never English prose.
var recorderResponseMsgKeyRe = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z0-9-]+)+$`)

func assertRecorderResponseMsgKey(t *testing.T, msg string) {
	t.Helper()
	if !recorderResponseMsgKeyRe.MatchString(msg) {
		t.Errorf("msg = %q, want an i18n translation key", msg)
	}
}

func decodeRecorderTaskMessage(t *testing.T, resp *http.Response) *plugnmeet.CommonResponse {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	res := new(plugnmeet.CommonResponse)
	if err = proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode CommonResponse: %v", err)
	}
	return res
}

func TestHandleRecorderTasksInvalidRequest(t *testing.T) {
	// the malformed-body guard runs before any dependency or logger is used
	rc := &RecordingController{}
	app := newRoomTestApp(rc.HandleRecorderTasks, roomTestLocals{isAdmin: true, roomId: "room-1"})

	res := decodeRecorderTaskMessage(t, sendRoomTestRequest(t, app, "not-a-protobuf-body"))
	if res.GetStatus() {
		t.Errorf("status = true, want false")
	}
	if res.GetMsg() != config.ErrRecordingInvalidRequest.Error() {
		t.Errorf("msg = %q, want %q", res.GetMsg(), config.ErrRecordingInvalidRequest.Error())
	}
	assertRecorderResponseMsgKey(t, res.GetMsg())
}

func TestHandleRecorderTasksRoomLookupFailure(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("failed to get sql db: %v", err)
	}
	// a closed connection makes the room lookup fail like a real DB outage
	if err = sqlDB.Close(); err != nil {
		t.Fatalf("failed to close test db: %v", err)
	}

	rc := &RecordingController{
		ds:     dbservice.New(dbservice.Args{Ctx: context.Background(), Db: gdb, Logger: logrus.New()}),
		logger: logrus.NewEntry(logrus.New()),
	}

	body, err := proto.Marshal(&plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}
	app := newRoomTestApp(rc.HandleRecorderTasks, roomTestLocals{isAdmin: true, roomId: "room-1", roomSid: "sid-1"})

	res := decodeRecorderTaskMessage(t, sendRoomTestRequest(t, app, string(body)))
	if res.GetStatus() {
		t.Errorf("status = true, want false")
	}
	if res.GetMsg() != config.ErrRecordingOperationFailed.Error() {
		t.Errorf("msg = %q, want %q", res.GetMsg(), config.ErrRecordingOperationFailed.Error())
	}
}

// newRecorderTaskRoomController builds a controller whose room lookup runs
// against a real room_info table. A nil room leaves the table empty.
func newRecorderTaskRoomController(t *testing.T, room *dbmodels.RoomInfo) *RecordingController {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	// the dbmodels tags target MySQL, so the test table is written by hand
	ddl := `CREATE TABLE room_info (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		room_title TEXT,
		roomId TEXT,
		sid TEXT,
		joined_participants INTEGER,
		is_running INTEGER,
		is_recording INTEGER,
		recorder_id TEXT,
		is_active_rtmp INTEGER,
		rtmp_node_id TEXT,
		webhook_url TEXT,
		is_breakout_room INTEGER,
		parent_room_id TEXT,
		creation_time INTEGER,
		created DATETIME,
		ended DATETIME,
		modified DATETIME
	)`
	if err = gdb.Exec(ddl).Error; err != nil {
		t.Fatalf("failed to create room_info test table: %v", err)
	}
	if room != nil {
		if err = gdb.Create(room).Error; err != nil {
			t.Fatalf("failed to insert test room: %v", err)
		}
	}

	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	return &RecordingController{
		ds:     dbservice.New(dbservice.Args{Ctx: context.Background(), Db: gdb, Logger: logger}),
		logger: logrus.NewEntry(logger),
	}
}

// TestHandleRecorderTasksResponseMessages guards the response contract of the
// recording endpoint: every branch answers with a config owned i18n key, never
// with English prose, and keeps the CommonResponse envelope.
func TestHandleRecorderTasksResponseMessages(t *testing.T) {
	activeRoom := func(mutate func(*dbmodels.RoomInfo)) *dbmodels.RoomInfo {
		room := &dbmodels.RoomInfo{RoomId: "room-1", Sid: "sid-1", IsRunning: 1}
		if mutate != nil {
			mutate(room)
		}
		return room
	}
	rtmpUrl := func(url string) *plugnmeet.RecordingReq {
		return &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RTMP, RtmpUrl: &url}
	}
	validUrl := "rtmp://example.com/live/stream"
	tokenLocals := roomTestLocals{isAdmin: true, roomId: "room-1", roomSid: "sid-1"}

	tests := []struct {
		name   string
		locals roomTestLocals
		room   *dbmodels.RoomInfo
		req    *plugnmeet.RecordingReq
		want   string
	}{
		{
			name:   "only admin",
			locals: roomTestLocals{roomId: "room-1", roomSid: "sid-1"},
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrUserOnlyAdmin.Error(),
		},
		{
			name:   "room id missing",
			locals: roomTestLocals{isAdmin: true, roomSid: "sid-1"},
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrRoomIdRequired.Error(),
		},
		{
			name:   "room sid mismatch",
			locals: roomTestLocals{isAdmin: true, roomId: "room-1", roomSid: "sid-1"},
			req:    &plugnmeet.RecordingReq{Sid: "other-sid", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrUserRoomSidMismatch.Error(),
		},
		{
			name:   "room not running",
			locals: tokenLocals,
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrAuthRoomNotActive.Error(),
		},
		{
			name:   "room id mismatch",
			locals: roomTestLocals{isAdmin: true, roomId: "other-room", roomSid: "sid-1"},
			room:   activeRoom(nil),
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrUserRoomIdMismatch.Error(),
		},
		{
			name:   "recording already running",
			locals: tokenLocals,
			room:   activeRoom(func(r *dbmodels.RoomInfo) { r.IsRecording = 1 }),
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RECORDING},
			want:   config.ErrRecordingAlreadyRunning.Error(),
		},
		{
			name:   "recording not running",
			locals: tokenLocals,
			room:   activeRoom(nil),
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_STOP_RECORDING},
			want:   config.ErrRecordingNotRunning.Error(),
		},
		{
			name:   "rtmp url required",
			locals: tokenLocals,
			room:   activeRoom(nil),
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_START_RTMP},
			want:   config.ErrRecordingRtmpUrlRequired.Error(),
		},
		{
			name:   "rtmp url invalid",
			locals: tokenLocals,
			room:   activeRoom(nil),
			req:    rtmpUrl("http://example.com/live/stream"),
			want:   config.ErrRecordingInvalidRtmpUrl.Error(),
		},
		{
			name:   "rtmp already running",
			locals: tokenLocals,
			room:   activeRoom(func(r *dbmodels.RoomInfo) { r.IsActiveRtmp = 1 }),
			req:    rtmpUrl(validUrl),
			want:   config.ErrRecordingRtmpAlreadyRunning.Error(),
		},
		{
			name:   "rtmp not running",
			locals: tokenLocals,
			room:   activeRoom(nil),
			req:    &plugnmeet.RecordingReq{Sid: "sid-1", Task: plugnmeet.RecordingTasks_STOP_RTMP},
			want:   config.ErrRecordingRtmpNotRunning.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertRecorderResponseMsgKey(t, tt.want)

			body, err := proto.Marshal(tt.req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}
			rc := newRecorderTaskRoomController(t, tt.room)
			app := newRoomTestApp(rc.HandleRecorderTasks, tt.locals)

			res := decodeRecorderTaskMessage(t, sendRoomTestRequest(t, app, string(body)))
			if res.GetStatus() {
				t.Errorf("status = true, want false")
			}
			if res.GetMsg() != tt.want {
				t.Errorf("msg = %q, want %q", res.GetMsg(), tt.want)
			}
			assertRecorderResponseMsgKey(t, res.GetMsg())
		})
	}
}

// TestRecorderTaskResponseMessagesAreI18nKeys pins the config owned response
// values of the endpoint, including the success envelope, to i18n keys.
func TestRecorderTaskResponseMessagesAreI18nKeys(t *testing.T) {
	msgs := []string{
		config.MsgSuccess,
		config.ErrUserOnlyAdmin.Error(),
		config.ErrRoomIdRequired.Error(),
		config.ErrUserRoomSidMismatch.Error(),
		config.ErrUserRoomIdMismatch.Error(),
		config.ErrAuthRoomNotActive.Error(),
		config.ErrRecordingInvalidRequest.Error(),
		config.ErrRecordingTaskInProgress.Error(),
		config.ErrRecordingRoomSidRequired.Error(),
		config.ErrRecordingRtmpUrlRequired.Error(),
		config.ErrRecordingInvalidRtmpUrl.Error(),
		config.ErrRecordingOperationFailed.Error(),
		config.ErrRecordingNoRecorder.Error(),
		config.ErrRecordingAlreadyRunning.Error(),
		config.ErrRecordingNotRunning.Error(),
		config.ErrRecordingRtmpAlreadyRunning.Error(),
		config.ErrRecordingRtmpNotRunning.Error(),
	}

	for _, msg := range msgs {
		assertRecorderResponseMsgKey(t, msg)
	}
}

func TestIsValidRtmpUrl(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{url: "rtmp://example.com/live/stream", want: true},
		{url: "rtmps://example.com:1936/live/stream", want: true},
		{url: "RTMPS://example.com/live/stream", want: true},
		{url: "http://example.com/live/stream", want: false},
		{url: "file:///etc/passwd", want: false},
		{url: "rtmp:///live/stream", want: false},
		{url: "example.com/live/stream", want: false},
		{url: "", want: false},
	}

	for _, tt := range tests {
		if got := isValidRtmpUrl(tt.url); got != tt.want {
			t.Errorf("isValidRtmpUrl(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
