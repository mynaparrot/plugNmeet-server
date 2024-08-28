package models

import (
	"database/sql"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"testing"
	"time"
)

var bbbm *BBBApiWrapperModel

func init() {
	appCnf, err := helpers.ReadYamlConfigFile(root + "/config.yaml")
	if err != nil {
		panic(err)
	}

	appCnf.RootWorkingDir = root
	// set this config for global usage
	config.New(appCnf)

	// now prepare server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		panic(err)
	}
	bbbm = NewBBBApiWrapperModel(nil, nil, nil)

	info := &dbmodels.RoomInfo{
		RoomId:       roomId,
		RoomTitle:    "Testing",
		Sid:          sid,
		IsRunning:    1,
		IsRecording:  0,
		IsActiveRtmp: 0,
		Created:      time.Now().UTC(),
	}

	_, err = analyticsModel.ds.InsertOrUpdateRoomInfo(info)
	if err != nil {
		panic(err)
	}

	v := sql.NullString{
		String: sid,
		Valid:  true,
	}
	data := &dbmodels.Recording{
		RecordID:         recordId,
		RoomID:           roomId,
		RoomSid:          v,
		Size:             10.10,
		RoomCreationTime: roomCreationTime,
	}

	_, err = analyticsModel.ds.InsertRecordingData(data)
	if err != nil {
		panic(err)
	}
}

func TestBBBApiWrapperModel_GetRecordings(t *testing.T) {
	recordings, pag, err := bbbm.GetRecordings("https://demo.plugnmeet.com", &bbbapiwrapper.GetRecordingsReq{
		MeetingID: roomId,
	})
	if err != nil {
		t.Error(err)
	}
	if len(recordings) == 0 {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v, %+v", recordings[0], *pag)

	recordings, pag, err = bbbm.GetRecordings("https://demo.plugnmeet.com", &bbbapiwrapper.GetRecordingsReq{
		RecordID: recordId,
	})
	if err != nil {
		t.Error(err)
	}
	if len(recordings) == 0 {
		t.Error("should contains some data but got empty")
	}

	t.Logf("%+v, %+v", recordings[0], *pag)
}
