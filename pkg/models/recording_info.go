package models

import (
	"fmt"
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *RecordingModel) FetchRecordings(r *plugnmeet.FetchRecordingsReq) (*plugnmeet.FetchRecordingsResult, error) {
	if r.Limit <= 0 {
		r.Limit = 20
	} else if r.Limit > 100 {
		r.Limit = 100
	}
	if r.OrderBy == "" {
		r.OrderBy = "DESC"
	}

	data, total, err := m.ds.GetRecordings(r.RoomIds, r.RoomSid, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}
	recordings := make([]*plugnmeet.RecordingInfo, 0, len(data))

	for _, v := range data {
		size, _ := strconv.ParseFloat(v.Size, 32)
		recording := &plugnmeet.RecordingInfo{
			RecordId:         v.RecordID,
			RoomId:           v.RoomID,
			RoomSid:          v.RoomSid.String,
			FilePath:         v.FilePath,
			FileSize:         helpers.ToFixed(float32(size), 2),
			CreationTime:     v.CreationTime,
			RoomCreationTime: v.RoomCreationTime,
		}
		if v.Metadata != "" {
			metadata := new(plugnmeet.RecordingMetadata)
			if err = protojson.Unmarshal([]byte(v.Metadata), metadata); err == nil {
				recording.Metadata = metadata
			}
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
func (m *RecordingModel) FetchRecording(recordId string) (*plugnmeet.RecordingInfo, error) {
	v, err := m.ds.GetRecording(recordId)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("no info found")
	}
	size, _ := strconv.ParseFloat(v.Size, 32)
	recording := &plugnmeet.RecordingInfo{
		RecordId:         v.RecordID,
		RoomId:           v.RoomID,
		RoomSid:          v.RoomSid.String,
		FilePath:         v.FilePath,
		FileSize:         helpers.ToFixed(float32(size), 2),
		CreationTime:     v.CreationTime,
		RoomCreationTime: v.RoomCreationTime,
	}
	if v.Metadata != "" {
		metadata := new(plugnmeet.RecordingMetadata)
		if err = protojson.Unmarshal([]byte(v.Metadata), metadata); err == nil {
			recording.Metadata = metadata
		}
	}

	return recording, nil
}

func (m *RecordingModel) RecordingInfo(req *plugnmeet.RecordingInfoReq) (*plugnmeet.RecordingInfoRes, error) {
	recording, err := m.FetchRecording(req.RecordId)
	if err != nil {
		return nil, err
	}

	pastRoomInfo := new(plugnmeet.PastRoomInfo)
	// SID can't be null, so we'll check before
	if recording.GetRoomSid() != "" {
		if roomInfo, err := m.ds.GetRoomInfoBySid(recording.GetRoomSid(), nil); err == nil && roomInfo != nil {
			pastRoomInfo = &plugnmeet.PastRoomInfo{
				RoomTitle:          roomInfo.RoomTitle,
				RoomId:             roomInfo.RoomId,
				RoomSid:            roomInfo.Sid,
				JoinedParticipants: roomInfo.JoinedParticipants,
				WebhookUrl:         roomInfo.WebhookUrl,
				Created:            roomInfo.Created.Format("2006-01-02 15:04:05"),
				Ended:              roomInfo.Ended.Format("2006-01-02 15:04:05"),
			}
			if an, err := m.ds.GetAnalyticByRoomTableId(roomInfo.ID); err == nil && an != nil {
				pastRoomInfo.AnalyticsFileId = &an.ArtifactId
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
