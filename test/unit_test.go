package test

import (
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/utils"
	"github.com/stretchr/testify/suite"
	"net/http"
	"testing"
)

type UnitTestSuite struct {
	suite.Suite
	roomInfo    *livekit.Room
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

func (s *UnitTestSuite) Test_03_isRoomActive() {
	res := prepareStringReq(http.MethodPost, "/auth/room/isRoomActive", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_04_getActiveRoomsInfo() {
	res := prepareStringReq(http.MethodPost, "/auth/room/getActiveRoomsInfo", "")
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_05_getActiveRoomInfo() {
	res := prepareStringReq(http.MethodPost, "/auth/room/getActiveRoomInfo", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_06_recorderTasks() {
	s.recordingId = test_recorderTasks(s.T(), s.roomInfo)
}

func (s *UnitTestSuite) Test_07_fetchRecordings() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/fetch", `{"room_ids":["room01"],"from":0,"limit":20,"order_by":"DESC"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_08_getDownloadToken() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/fetch", `{"record_id":"`+s.recordingId+`"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_10_deleteRecording() {
	res := prepareStringReq(http.MethodPost, "/auth/recording/delete", `{"record_id":"`+s.recordingId+`"}`)
	performCommonReq(s.T(), res, true)
}

func (s *UnitTestSuite) Test_11_webhooks() {
	test_webhooks(s.T(), s.roomInfo)
}

func (s *UnitTestSuite) Test_99_endRoom() {
	s.Test_01_CreateRoom()

	res := prepareStringReq(http.MethodPost, "/auth/room/endRoom", `{"room_id":"room01"}`)
	performCommonReq(s.T(), res, true)
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}
