package main

import (
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func Test_Auth(t *testing.T) {
	test_prepareServer(t)
	test_roomAuth(t)
}

func test_roomAuth(t *testing.T) {
	tests := []struct {
		name   string
		method string
		route  string
		body   string
	}{
		{
			name:   "create",
			method: http.MethodPost,
			route:  "/auth/room/create",
			body:   `{"room_id":"room01","metadata":{"room_title":"Test room","welcome_message":"Welcome to room","room_features":{"allow_webcams":true,"mute_on_start":false,"allow_screen_share":true,"allow_recording":true,"allow_rtmp":true,"admin_only_webcams":false,"allow_view_other_webcams":true,"allow_view_other_users_list":true,"allow_polls":true,"room_duration":0,"chat_features":{"allow_chat":true,"allow_file_upload":true},"shared_note_pad_features":{"allowed_shared_note_pad":true},"whiteboard_features":{"allowed_whiteboard":true},"external_media_player_features":{"allowed_external_media_player":true},"waiting_room_features":{"is_active":false},"breakout_room_features":{"is_allow":true,"allowed_number_rooms":2},"display_external_link_features":{"is_allow":true}},"default_lock_settings":{"lock_microphone":false,"lock_webcam":false,"lock_screen_sharing":true,"lock_whiteboard":true,"lock_shared_notepad":true,"lock_chat":false,"lock_chat_send_message":false,"lock_chat_file_share":false,"lock_private_chat":false}}}`,
		},
		{
			name:   "getJoinToken",
			method: http.MethodPost,
			route:  "/auth/room/getJoinToken",
			body:   `{"room_id":"room01","user_info":{"name":"Test","user_id":"test001","is_admin":true,"is_hidden":false}}`,
		},
		{
			name:   "isRoomActive",
			method: http.MethodPost,
			route:  "/auth/room/isRoomActive",
			body:   `{"room_id":"room01"}`,
		},
		{
			name:   "getActiveRoomsInfo",
			method: http.MethodPost,
			route:  "/auth/room/getActiveRoomsInfo",
		},
		{
			name:   "getActiveRoomInfo",
			method: http.MethodPost,
			route:  "/auth/room/getActiveRoomInfo",
			body:   `{"room_id":"room01"}`,
		},
		{
			name:   "endRoom",
			method: http.MethodPost,
			route:  "/auth/room/endRoom",
			body:   `{"room_id":"room01"}`,
		},
	}
	router := Router()

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.route, strings.NewReader(tt.body))
		req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
		req.Header.Set("API-SECRET", config.AppCnf.Client.Secret)
		req.Header.Set("Content-Type", "application/json")

		t.Run(tt.name, func(t *testing.T) {
			res, err := router.Test(req)
			if err != nil {
				t.Error(err)
			}

			if res.StatusCode != 200 {
				t.Errorf("Error in router: %s, Error code: %d", tt.route, res.StatusCode)
			}

			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Error(err)
			}

			rr := new(plugnmeet.CommonResponse)
			err = json.Unmarshal(body, rr)
			if err != nil {
				t.Error(err)
			}

			if !rr.Status {
				t.Errorf("Error in router: %s, Error msg: %s", tt.route, rr.Msg)
			}
		})
	}
}

func test_prepareServer(t *testing.T) {
	tests := []struct {
		name       string
		configPath string
		wantErr    bool
	}{
		{
			name:       "prepareServer",
			configPath: "../../test/config.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := prepareServer(tt.configPath); (err != nil) != tt.wantErr {
				t.Errorf("prepareServer() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
