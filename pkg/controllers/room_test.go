package controllers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/proto"
)

// roomTestLocals mirrors the values AuthController puts into fiber.Locals.
type roomTestLocals struct {
	isAdmin         bool
	roomId          string
	roomSid         string
	requestedUserId string
}

func newRoomTestApp(handler fiber.Handler, l roomTestLocals) *fiber.App {
	app := fiber.New()
	app.Post("/test", func(c fiber.Ctx) error {
		c.Locals("isAdmin", l.isAdmin)
		c.Locals("roomId", l.roomId)
		c.Locals("roomSid", l.roomSid)
		c.Locals("requestedUserId", l.requestedUserId)
		return c.Next()
	}, handler)
	return app
}

func sendRoomTestRequest(t *testing.T, app *fiber.App, body string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/protobuf")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

// decodeRoomCommonError asserts the failure contract of the room endpoints: a
// non-2xx HTTP status and a CommonResponse body carrying the expected i18n key.
func decodeRoomCommonError(t *testing.T, resp *http.Response, wantHTTPStatus int, wantMsg string) {
	t.Helper()
	if resp.StatusCode != wantHTTPStatus {
		t.Fatalf("http status = %d, want %d", resp.StatusCode, wantHTTPStatus)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	res := new(plugnmeet.CommonResponse)
	if err = proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode CommonResponse: %v", err)
	}
	if res.GetStatus() {
		t.Errorf("status = true, want false")
	}
	if res.GetMsg() != wantMsg {
		t.Errorf("msg = %q, want %q", res.GetMsg(), wantMsg)
	}
	if res.GetStatusCode() == plugnmeet.StatusCode_SUCCESS {
		t.Errorf("status_code = SUCCESS, want a failure status code")
	}
}

func TestRoomControllerAdminOnlyGuards(t *testing.T) {
	rc := &RoomController{}

	tests := []struct {
		name string
		h    fiber.Handler
	}{
		{name: "end room", h: rc.HandleEndRoomForAPI},
		{name: "change visibility", h: rc.HandleChangeVisibilityForAPI},
		{name: "enable sip dial in", h: rc.HandleEnableRoomSipDialIn},
		{name: "create ingress", h: rc.HandleCreateIngress},
		{name: "external display link", h: rc.HandleExternalDisplayLink},
		{name: "external media player", h: rc.HandleExternalMediaPlayer},
		{name: "approve waiting users", h: rc.HandleApproveUsers},
		{name: "update waiting room message", h: rc.HandleUpdateWaitingRoomMessage},
		{name: "shared notepad", h: rc.HandleChangeSharedNotepadStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newRoomTestApp(tt.h, roomTestLocals{isAdmin: false, roomId: "room-1"})
			// the admin guard runs before the body is decoded
			decodeRoomCommonError(t, sendRoomTestRequest(t, app, "not-a-protobuf-body"), http.StatusForbidden, config.ErrRoomOnlyAdmin.Error())
		})
	}
}

func TestRoomControllerInvalidRequest(t *testing.T) {
	rc := &RoomController{}
	app := newRoomTestApp(rc.HandleChangeVisibilityForAPI, roomTestLocals{isAdmin: true, roomId: "room-1"})

	decodeRoomCommonError(t, sendRoomTestRequest(t, app, "not-a-protobuf-body"), http.StatusBadRequest, config.ErrRoomInvalidRequest.Error())
}

func TestRoomControllerRoomIdMismatch(t *testing.T) {
	rc := &RoomController{}
	body, err := proto.Marshal(&plugnmeet.ChangeVisibilityRes{RoomId: "another-room"})
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	app := newRoomTestApp(rc.HandleChangeVisibilityForAPI, roomTestLocals{isAdmin: true, roomId: "room-1"})
	decodeRoomCommonError(t, sendRoomTestRequest(t, app, string(body)), http.StatusForbidden, config.ErrRoomIdMismatch.Error())
}
