package test

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"net/http"
	"testing"
)

func test_HandleCreateEtherpad(t *testing.T, token string) {
	t.Run("CreateEtherpad", func(t *testing.T) {
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/etherpad/create", nil)
		performCommonProtoReq(t, req, true)
	})
}

func test_HandleChangeStatus(t *testing.T, token string, roomInfo *livekit.Room) {
	b := new(plugnmeet.ChangeEtherpadStatusReq)
	b.RoomId = roomInfo.Name

	t.Run("changeStatus_active", func(t *testing.T) {
		b.IsActive = true
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/etherpad/changeStatus", b)
		performCommonProtoReq(t, req, true)
	})

	t.Run("changeStatus_deactivate", func(t *testing.T) {
		b.IsActive = false
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/etherpad/changeStatus", b)
		performCommonProtoReq(t, req, true)
	})
}
