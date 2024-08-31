package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"testing"
)

func TestBBBApiWrapperModel_GetRecordings(t *testing.T) {
	bbbm := NewBBBApiWrapperModel(nil, nil, nil)
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
