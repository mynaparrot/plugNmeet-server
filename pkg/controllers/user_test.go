package controllers

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/proto"
)

// The user controller guards only read fiber.Locals and the request body, so the
// dependency-heavy fields stay nil here.
func newUserTestApp(handler fiber.Handler, l roomTestLocals) *fiber.App {
	return newRoomTestApp(handler, l)
}

func TestUserControllerAdminOnlyGuards(t *testing.T) {
	uc := &UserController{}

	tests := []struct {
		name string
		h    fiber.Handler
	}{
		{name: "update lock settings", h: uc.HandleUpdateUserLockSetting},
		{name: "mute unmute track", h: uc.HandleMuteUnMuteTrack},
		{name: "remove participant", h: uc.HandleRemoveParticipant},
		{name: "switch presenter", h: uc.HandleSwitchPresenter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newUserTestApp(tt.h, roomTestLocals{isAdmin: false, roomId: "room-1"})
			decodeRoomCommonError(t, sendRoomTestRequest(t, app, "not-a-protobuf-body"), http.StatusForbidden, config.ErrUserOnlyAdmin.Error())
		})
	}
}

func TestUserControllerInvalidRequest(t *testing.T) {
	uc := &UserController{}
	app := newUserTestApp(uc.HandleRemoveParticipant, roomTestLocals{isAdmin: true, roomId: "room-1"})

	decodeRoomCommonError(t, sendRoomTestRequest(t, app, "not-a-protobuf-body"), http.StatusBadRequest, config.ErrUserInvalidRequest.Error())
}

func TestUserControllerScopeMismatch(t *testing.T) {
	uc := &UserController{}

	t.Run("room id mismatch", func(t *testing.T) {
		body, err := proto.Marshal(&plugnmeet.RemoveParticipantReq{RoomId: "another-room", UserId: "user-2"})
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		app := newUserTestApp(uc.HandleRemoveParticipant, roomTestLocals{isAdmin: true, roomId: "room-1", requestedUserId: "user-1"})
		decodeRoomCommonError(t, sendRoomTestRequest(t, app, string(body)), http.StatusForbidden, config.ErrUserRoomIdMismatch.Error())
	})

	t.Run("remove self", func(t *testing.T) {
		body, err := proto.Marshal(&plugnmeet.RemoveParticipantReq{RoomId: "room-1", UserId: "user-1"})
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		app := newUserTestApp(uc.HandleRemoveParticipant, roomTestLocals{isAdmin: true, roomId: "room-1", requestedUserId: "user-1"})
		decodeRoomCommonError(t, sendRoomTestRequest(t, app, string(body)), http.StatusForbidden, config.ErrUserCannotRemoveSelf.Error())
	})

	t.Run("room sid mismatch", func(t *testing.T) {
		body, err := proto.Marshal(&plugnmeet.UpdateUserLockSettingsReq{RoomId: "room-1", RoomSid: "sid-from-client"})
		if err != nil {
			t.Fatalf("failed to marshal request: %v", err)
		}
		app := newUserTestApp(uc.HandleUpdateUserLockSetting, roomTestLocals{isAdmin: true, roomId: "room-1", roomSid: "sid-from-token", requestedUserId: "user-1"})
		decodeRoomCommonError(t, sendRoomTestRequest(t, app, string(body)), http.StatusForbidden, config.ErrUserRoomSidMismatch.Error())
	})
}
