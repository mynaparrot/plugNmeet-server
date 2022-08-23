package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func Test_prepareServer(t *testing.T) {
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

func Test_Auth(t *testing.T) {
	// create room first
	rInfo := createRoom(t)

	// now perform different recorder tasks
	rId := test_recorderTasks(t, rInfo)

	// webhook test
	test_webhooks(t, rInfo)

	// different room auth tasks
	test_roomAuth(t)

	// different recorder auth tasks
	test_recordingAuth(t, rId)
}

type commonTest struct {
	name           string
	method         string
	route          string
	body           string
	expectedStatus bool
}

func test_roomAuth(t *testing.T) {
	tests := []commonTest{
		{
			name:           "create",
			method:         http.MethodPost,
			route:          "/auth/room/create",
			body:           `{"room_id":"room01","metadata":{"room_title":"Test room","welcome_message":"Welcome to room","room_features":{"allow_webcams":true,"mute_on_start":false,"allow_screen_share":true,"allow_recording":true,"allow_rtmp":true,"admin_only_webcams":false,"allow_view_other_webcams":true,"allow_view_other_users_list":true,"allow_polls":true,"room_duration":0,"chat_features":{"allow_chat":true,"allow_file_upload":true},"shared_note_pad_features":{"allowed_shared_note_pad":true},"whiteboard_features":{"allowed_whiteboard":true},"external_media_player_features":{"allowed_external_media_player":true},"waiting_room_features":{"is_active":false},"breakout_room_features":{"is_allow":true,"allowed_number_rooms":2},"display_external_link_features":{"is_allow":true}},"default_lock_settings":{"lock_microphone":false,"lock_webcam":false,"lock_screen_sharing":true,"lock_whiteboard":true,"lock_shared_notepad":true,"lock_chat":false,"lock_chat_send_message":false,"lock_chat_file_share":false,"lock_private_chat":false}}}`,
			expectedStatus: true,
		},
		{
			name:           "getJoinToken",
			method:         http.MethodPost,
			route:          "/auth/room/getJoinToken",
			body:           `{"room_id":"room01","user_info":{"name":"Test","user_id":"test001","is_admin":true,"is_hidden":false}}`,
			expectedStatus: true,
		},
		{
			name:           "isRoomActive",
			method:         http.MethodPost,
			route:          "/auth/room/isRoomActive",
			body:           `{"room_id":"room01"}`,
			expectedStatus: true,
		},
		{
			name:           "getActiveRoomsInfo",
			method:         http.MethodPost,
			route:          "/auth/room/getActiveRoomsInfo",
			expectedStatus: true,
		},
		{
			name:           "getActiveRoomInfo",
			method:         http.MethodPost,
			route:          "/auth/room/getActiveRoomInfo",
			body:           `{"room_id":"room01"}`,
			expectedStatus: true,
		},
		{
			name:           "endRoom",
			method:         http.MethodPost,
			route:          "/auth/room/endRoom",
			body:           `{"room_id":"room01"}`,
			expectedStatus: true,
		},
	}

	doAuthTests(t, tests)
}

func test_recordingAuth(t *testing.T, rId string) {
	tests := []commonTest{
		{
			name:           "fetch",
			method:         http.MethodPost,
			route:          "/auth/recording/fetch",
			body:           `{"room_ids":["room01"],"from":0,"limit":20,"order_by":"DESC"}`,
			expectedStatus: true,
		},
		{
			name:           "getDownloadToken",
			method:         http.MethodPost,
			route:          "/auth/recording/getDownloadToken",
			body:           `{"record_id":"` + rId + `"}`,
			expectedStatus: true,
		},
		{
			name:           "delete",
			method:         http.MethodPost,
			route:          "/auth/recording/delete",
			body:           `{"record_id":"` + rId + `"}`,
			expectedStatus: true,
		},
	}
	doAuthTests(t, tests)
}

func test_recorderTasks(t *testing.T, rInfo *livekit.Room) string {
	rid := fmt.Sprintf("%s-%d", rInfo.Sid, time.Now().UnixMilli())
	body := &plugnmeet.RecorderToPlugNmeet{
		From:        "recorder",
		Status:      true,
		Msg:         "success",
		RecordingId: rid,
		RoomId:      rInfo.Name,
		RoomSid:     rInfo.Sid,
		RecorderId:  "node_01",
		FilePath:    fmt.Sprintf("%s/node_01/%s.mp4", config.AppCnf.RecorderInfo.RecordingFilesPath, rid),
		FileSize:    10,
	}

	tests := []struct {
		task plugnmeet.RecordingTasks
	}{
		{
			task: plugnmeet.RecordingTasks_START_RECORDING,
		},
		{
			task: plugnmeet.RecordingTasks_STOP_RECORDING,
		},
		{
			task: plugnmeet.RecordingTasks_START_RTMP,
		},
		{
			task: plugnmeet.RecordingTasks_STOP_RTMP,
		},
		{
			task: plugnmeet.RecordingTasks_RECORDING_PROCEEDED,
		},
	}

	for _, tt := range tests {
		body.Task = tt.task
		marshal, err := proto.Marshal(body)
		if err != nil {
			t.Error(err)
		}

		req := httptest.NewRequest(http.MethodPost, "/auth/recorder/notify", bytes.NewReader(marshal))
		req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
		req.Header.Set("API-SECRET", config.AppCnf.Client.Secret)
		req.Header.Set("Content-Type", "application/protobuf")

		t.Run(tt.task.String(), func(t *testing.T) {
			router := Router()
			res, err := router.Test(req)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != 200 {
				t.Errorf("Error in router: %s, Error code: %d", "/auth/room/create", res.StatusCode)
			}
		})
	}

	return rid
}

func test_webhooks(t *testing.T, rInfo *livekit.Room) {
	body := &livekit.WebhookEvent{
		Room: rInfo,
		Participant: &livekit.ParticipantInfo{
			Name:     "Test",
			Identity: "test001",
		},
	}

	tests := []struct {
		event string
	}{
		{
			event: "room_started",
		},
		{
			event: "participant_joined",
		},
		{
			event: "participant_left",
		},
		{
			event: "room_finished",
		},
	}

	for _, tt := range tests {
		body.Event = tt.event

		var encoded []byte
		var err error

		encoded, err = json.Marshal(body)
		if err != nil {
			t.Error(err)
		}
		// sign payload
		sum := sha256.Sum256(encoded)
		b64 := base64.StdEncoding.EncodeToString(sum[:])

		at := auth.NewAccessToken(config.AppCnf.LivekitInfo.ApiKey, config.AppCnf.LivekitInfo.Secret).
			SetValidFor(5 * time.Minute).
			SetSha256(b64)
		token, err := at.ToJWT()

		if err != nil {
			t.Error(err)
		}

		t.Run(body.Event, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(encoded))
			req.Header.Set("Authorization", token)
			req.Header.Set("Content-Type", "application/json")

			router := Router()
			res, err := router.Test(req)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != 200 {
				t.Errorf("Error in router: %s, Error code: %d", "/webhook", res.StatusCode)
			}
		})

	}
}

func doAuthTests(t *testing.T, c []commonTest) {
	router := Router()

	for _, tt := range c {
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

			if rr.Status != tt.expectedStatus {
				t.Errorf("Error in router: %s, Expected: %t, Got: %t,Msg: %s", tt.route, tt.expectedStatus, rr.Status, rr.Msg)
			}
		})
	}
}

func createRoom(t *testing.T) *livekit.Room {
	const body = `{"room_id":"room01","metadata":{"room_title":"Test room","welcome_message":"Welcome to room","room_features":{"allow_webcams":true,"mute_on_start":false,"allow_screen_share":true,"allow_recording":true,"allow_rtmp":true,"admin_only_webcams":false,"allow_view_other_webcams":true,"allow_view_other_users_list":true,"allow_polls":true,"room_duration":0,"chat_features":{"allow_chat":true,"allow_file_upload":true},"shared_note_pad_features":{"allowed_shared_note_pad":true},"whiteboard_features":{"allowed_whiteboard":true},"external_media_player_features":{"allowed_external_media_player":true},"waiting_room_features":{"is_active":false},"breakout_room_features":{"is_allow":true,"allowed_number_rooms":2},"display_external_link_features":{"is_allow":true}},"default_lock_settings":{"lock_microphone":false,"lock_webcam":false,"lock_screen_sharing":true,"lock_whiteboard":true,"lock_shared_notepad":true,"lock_chat":false,"lock_chat_send_message":false,"lock_chat_file_share":false,"lock_private_chat":false}}}`

	var rInfo *livekit.Room

	req := httptest.NewRequest(http.MethodPost, "/auth/room/create", strings.NewReader(body))
	req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
	req.Header.Set("API-SECRET", config.AppCnf.Client.Secret)
	req.Header.Set("Content-Type", "application/json")

	t.Run("createRoom", func(t *testing.T) {
		router := Router()
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
		err = json.Unmarshal(b, rr)
		if err != nil {
			t.Error(err)
		}

		if rr.Status != true {
			t.Error(rr.Status)
		}

		rInfo = rr.RoomInfo
	})

	return rInfo
}
