package test

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"net/http"
	"testing"
)

func test_recording(t *testing.T, token string, roomInfo *livekit.Room) {
	m := new(plugnmeet.RecordingReq)
	m.Sid = roomInfo.Sid

	t.Run(plugnmeet.RecordingTasks_START_RECORDING.String(), func(t *testing.T) {
		m.Task = plugnmeet.RecordingTasks_START_RECORDING
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/recording", m)
		// recording don't start if recorder online. So, we'll expect false
		performCommonProtoReq(t, req, false)
	})

	t.Run(plugnmeet.RecordingTasks_STOP_RECORDING.String(), func(t *testing.T) {
		m.Task = plugnmeet.RecordingTasks_STOP_RECORDING
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/recording", m)
		performCommonProtoReq(t, req, false)
	})
}

func test_rtmp(t *testing.T, token string, roomInfo *livekit.Room) {
	m := new(plugnmeet.RecordingReq)
	m.Sid = roomInfo.Sid

	t.Run(plugnmeet.RecordingTasks_START_RTMP.String(), func(t *testing.T) {
		m.Task = plugnmeet.RecordingTasks_START_RTMP
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/recording", m)
		// recording don't start if recorder online. So, we'll expect false
		performCommonProtoReq(t, req, false)
	})

	t.Run(plugnmeet.RecordingTasks_STOP_RTMP.String(), func(t *testing.T) {
		m.Task = plugnmeet.RecordingTasks_STOP_RTMP
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/recording", m)
		performCommonProtoReq(t, req, false)
	})
}

func test_updateLockSettings(t *testing.T, token string, roomInfo *livekit.Room) {
	m := new(plugnmeet.UpdateUserLockSettingsReq)
	m.RoomSid = roomInfo.Sid
	m.RoomId = roomInfo.Name
	m.UserId = "test001"
	m.RequestedUserId = "dummy_admin"

	t.Run("mic_lock", func(t *testing.T) {
		m.Direction = "lock"
		m.Service = "mic"
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/updateLockSettings", m)
		performCommonProtoReq(t, req, true)
	})

	t.Run("mic_unlock", func(t *testing.T) {
		m.Direction = "unlock"
		m.Service = "mic"
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/updateLockSettings", m)
		performCommonProtoReq(t, req, true)
	})

	t.Run("sendChatMsg_lock", func(t *testing.T) {
		m.Direction = "lock"
		m.Service = "sendChatMsg"
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/updateLockSettings", m)
		performCommonProtoReq(t, req, true)
	})

	t.Run("sendChatMsg_unlock", func(t *testing.T) {
		m.Direction = "unlock"
		m.Service = "sendChatMsg"
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/updateLockSettings", m)
		performCommonProtoReq(t, req, true)
	})
}
