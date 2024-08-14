package test

import (
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/handler"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"testing"
)

func test_HandleRoomCreate(t *testing.T) *livekit.Room {
	const body = `{"room_id":"room01","metadata":{"room_title":"Test room","welcome_message":"Welcome to room","room_features":{"allow_webcams":true,"mute_on_start":false,"allow_screen_share":true,"allow_recording":true,"allow_rtmp":true,"admin_only_webcams":false,"allow_view_other_webcams":true,"allow_view_other_users_list":true,"allow_polls":true,"room_duration":0,"chat_features":{"allow_chat":true,"allow_file_upload":true},"shared_note_pad_features":{"allowed_shared_note_pad":true},"whiteboard_features":{"allowed_whiteboard":true},"external_media_player_features":{"allowed_external_media_player":true},"waiting_room_features":{"is_active":false},"breakout_room_features":{"is_allow":true,"allowed_number_rooms":2},"display_external_link_features":{"is_allow":true}},"default_lock_settings":{"lock_microphone":false,"lock_webcam":false,"lock_screen_sharing":true,"lock_whiteboard":true,"lock_shared_notepad":true,"lock_chat":false,"lock_chat_send_message":false,"lock_chat_file_share":false,"lock_private_chat":false}}}`

	req := prepareStringReq(http.MethodPost, "/auth/room/create", body)

	router := handler.Router()
	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Error in router: %s, Error code: %d", "/auth/room/create", res.StatusCode)
	}

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if err != nil {
		t.Error(err)
	}

	rr := new(plugnmeet.CreateRoomRes)
	err = protojson.Unmarshal(b, rr)
	if err != nil {
		t.Error(err)
	}

	if rr.Status != true {
		t.Error(rr.Status)
	}

	return rr.RoomInfo
}

func test_HandleJoinToken(t *testing.T) string {
	const body = `{"room_id":"room01","user_info":{"name":"Test","user_id":"test001","is_admin":true,"is_hidden":false}}`

	req := prepareStringReq(http.MethodPost, "/auth/room/getJoinToken", body)

	router := handler.Router()
	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Error in router: %s, Error code: %d", "/auth/room/create", res.StatusCode)
	}

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if err != nil {
		t.Error(err)
	}

	rr := new(plugnmeet.GenerateTokenRes)
	err = json.Unmarshal(b, rr)
	if err != nil {
		t.Error(err)
	}

	if rr.Status != true {
		t.Error(rr.Status)
		return ""
	}

	return *rr.Token
}

func test_verifyToken(t *testing.T, token string) *plugnmeet.VerifyTokenRes {
	req := prepareStringWithTokenReq(token, http.MethodPost, "/api/verifyToken", nil)

	router := handler.Router()
	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
	}
	if res.StatusCode != 200 {
		t.Errorf("Error in router: %s, Error code: %d", "/api/verifyToken", res.StatusCode)
	}

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}
	if err != nil {
		t.Error(err)
	}

	rr := new(plugnmeet.VerifyTokenRes)
	err = proto.Unmarshal(b, rr)
	if err != nil {
		t.Error(err)
	}

	if rr.Status != true {
		t.Error(rr.Status)
		return nil
	}

	return rr
}
