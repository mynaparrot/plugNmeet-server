package test

import (
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/mynaparrot/plugnmeet-server/pkg/utils"
	"github.com/stretchr/testify/suite"
	"net/http"
	"testing"
)

type UnitTestSuite struct {
	suite.Suite
	roomInfo    *livekit.Room
	room        *lksdk.Room
	token       string
	recordingId string
}

func (s *UnitTestSuite) Test_00_PrepareServer() {
	const configPath = "./config.yaml"

	if err := utils.PrepareServer(configPath); err != nil {
		s.T().Errorf("prepareServer() error = %v", err)
	}
}

func (s *UnitTestSuite) Test_01_CreateRoom() {
	s.roomInfo = test_HandleRoomCreate(s.T())
}

func (s *UnitTestSuite) Test_02_getJoinToken() {
	s.token = test_HandleJoinToken(s.T())
}

func (s *UnitTestSuite) Test_03_verifyToken() {
	res := test_verifyToken(s.T(), s.token)
	if res != nil {
		s.room = connectLivekit(s.T(), *res.Token, *res.LivekitHost)
	}
}

func (s *UnitTestSuite) Test_04_commonAPI() {
	test_recording(s.T(), s.token, s.roomInfo)
	test_rtmp(s.T(), s.token, s.roomInfo)
	test_updateLockSettings(s.T(), s.token, s.roomInfo)
}

func (s *UnitTestSuite) Test_05_isRoomActive() {
	res := prepareStringReq(http.MethodPost, "/auth/room/isRoomActive", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_06_getActiveRoomsInfo() {
	res := prepareStringReq(http.MethodPost, "/auth/room/getActiveRoomsInfo", "")
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_07_getActiveRoomInfo() {
	res := prepareStringReq(http.MethodPost, "/auth/room/getActiveRoomInfo", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_08_recorderTasks() {
	s.recordingId = test_recorderTasks(s.T(), s.roomInfo)
}

func (s *UnitTestSuite) Test_09_fetchRecordings() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/fetch", `{"room_ids":["room01"],"from":0,"limit":20,"order_by":"DESC"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_10_getDownloadToken() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/fetch", `{"record_id":"`+s.recordingId+`"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_11_deleteRecording() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/delete", `{"record_id":"`+s.recordingId+`"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_12_webhooks() {
	test_webhooks(s.T(), s.roomInfo, false)
}

func (s *UnitTestSuite) Test_99_endRoom() {
	if s.room != nil {
		s.room.Disconnect()
	}
	res := prepareStringReq(http.MethodPost, "/auth/room/endRoom", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)

	test_webhooks(s.T(), s.roomInfo, true)
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}
