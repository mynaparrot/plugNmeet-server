package recordingmodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (a *AuthRecording) FetchRecordings(r *plugnmeet.FetchRecordingsReq) (*plugnmeet.FetchRecordingsResult, error) {
	data, total, err := a.ds.GetRecordings(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}
	var recordings []*plugnmeet.RecordingInfo
	for _, v := range data {
		recording := &plugnmeet.RecordingInfo{
			RecordId:         v.RecordID,
			RoomId:           v.RoomID,
			RoomSid:          v.RoomSid.String,
			FilePath:         v.FilePath,
			FileSize:         float32(v.Size),
			CreationTime:     v.CreationTime,
			RoomCreationTime: v.RoomCreationTime,
		}
		recordings = append(recordings, recording)
	}

	result := &plugnmeet.FetchRecordingsResult{
		TotalRecordings: total,
		From:            r.From,
		Limit:           r.Limit,
		OrderBy:         r.OrderBy,
		RecordingsList:  recordings,
	}

	return result, nil
}

// FetchRecording to get single recording information from DB
func (a *AuthRecording) FetchRecording(recordId string) (*plugnmeet.RecordingInfo, error) {
	v, err := a.ds.GetRecording(recordId)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, errors.New("no info found")
	}
	recording := &plugnmeet.RecordingInfo{
		RecordId:         v.RecordID,
		RoomId:           v.RoomID,
		RoomSid:          v.RoomSid.String,
		FilePath:         v.FilePath,
		FileSize:         float32(v.Size),
		CreationTime:     v.CreationTime,
		RoomCreationTime: v.RoomCreationTime,
	}

	return recording, nil
}

func (a *AuthRecording) RecordingInfo(req *plugnmeet.RecordingInfoReq) (*plugnmeet.RecordingInfoRes, error) {
	recording, err := a.FetchRecording(req.RecordId)
	if err != nil {
		return nil, err
	}

	pastRoomInfo := new(plugnmeet.PastRoomInfo)
	// SID can't be null, so we'll check before
	if recording.GetRoomSid() != "" {
		if roomInfo, err := a.ds.GetRoomInfoBySid(recording.GetRoomSid(), nil); err == nil && roomInfo != nil {
			pastRoomInfo = &plugnmeet.PastRoomInfo{
				RoomTitle:          roomInfo.RoomTitle,
				RoomId:             roomInfo.RoomId,
				RoomSid:            roomInfo.Sid,
				JoinedParticipants: roomInfo.JoinedParticipants,
				WebhookUrl:         roomInfo.WebhookUrl,
				Created:            roomInfo.Created.Format("2006-01-02 15:04:05"),
				Ended:              roomInfo.Ended.Format("2006-01-02 15:04:05"),
			}
			if an, err := a.ds.GetAnalyticByRoomTableId(roomInfo.ID); err == nil && an != nil {
				pastRoomInfo.AnalyticsFileId = an.FileID
			}
		}
	}

	return &plugnmeet.RecordingInfoRes{
		Status:        true,
		Msg:           "success",
		RecordingInfo: recording,
		RoomInfo:      pastRoomInfo,
	}, nil
}
