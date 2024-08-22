package roommodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"time"
)

func (m *RoomModel) IsRoomActive(r *plugnmeet.IsRoomActiveReq) (*plugnmeet.IsRoomActiveRes, *plugnmeet.RoomMetadata) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	res := &plugnmeet.IsRoomActiveRes{
		Status: true,
		Msg:    "room is not active",
	}

	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if err != nil {
		res.Status = false
		res.Msg = err.Error()
		return res, nil
	}
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return res, nil
	}

	// let's make sure room actually active
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		res.Status = false
		res.Msg = err.Error()
		return res, nil
	}

	if rInfo == nil || meta == nil {
		// Room isn't active. Change status
		_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
			RoomId:    r.RoomId,
			IsRunning: 0,
			Ended:     time.Now().UTC(),
		})
		return res, nil
	}

	res.IsActive = true
	res.Msg = "room is active"
	return res, meta
}

func (m *RoomModel) GetActiveRoomInfo(r *plugnmeet.GetActiveRoomInfoReq) (bool, string, *plugnmeet.ActiveRoomWithParticipant) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	roomDbInfo, _ := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "no room found", nil
	}

	rrr, err := m.lk.LoadRoomInfo(r.RoomId)
	if err != nil {
		return false, err.Error(), nil
	}

	res := new(plugnmeet.ActiveRoomWithParticipant)
	res.RoomInfo = &plugnmeet.ActiveRoomInfo{
		RoomTitle:          roomDbInfo.RoomTitle,
		RoomId:             roomDbInfo.RoomId,
		Sid:                roomDbInfo.Sid,
		JoinedParticipants: roomDbInfo.JoinedParticipants,
		IsRunning:          int32(roomDbInfo.IsRunning),
		IsRecording:        int32(roomDbInfo.IsRecording),
		IsActiveRtmp:       int32(roomDbInfo.IsActiveRtmp),
		WebhookUrl:         roomDbInfo.WebhookUrl,
		IsBreakoutRoom:     int32(roomDbInfo.IsBreakoutRoom),
		ParentRoomId:       roomDbInfo.ParentRoomID,
		CreationTime:       roomDbInfo.CreationTime,
		Metadata:           rrr.Metadata,
	}
	res.ParticipantsInfo, _ = m.lk.LoadParticipants(roomDbInfo.RoomId)

	return true, "success", res
}

func (m *RoomModel) GetActiveRoomsInfo() (bool, string, []*plugnmeet.ActiveRoomWithParticipant) {
	roomsInfo, err := m.ds.GetActiveRoomsInfo()
	if err != nil {
		return false, err.Error(), nil
	}
	if roomsInfo != nil || len(roomsInfo) == 0 {
		return false, "no active room found", nil
	}

	if len(roomsInfo) == 0 {
		return false, "no active room found", nil
	}

	var res []*plugnmeet.ActiveRoomWithParticipant
	for _, r := range roomsInfo {
		i := &plugnmeet.ActiveRoomWithParticipant{
			RoomInfo: &plugnmeet.ActiveRoomInfo{
				RoomTitle:          r.RoomTitle,
				RoomId:             r.RoomId,
				Sid:                r.Sid,
				JoinedParticipants: r.JoinedParticipants,
				IsRunning:          int32(r.IsRunning),
				IsRecording:        int32(r.IsRecording),
				IsActiveRtmp:       int32(r.IsActiveRtmp),
				WebhookUrl:         r.WebhookUrl,
				IsBreakoutRoom:     int32(r.IsBreakoutRoom),
				ParentRoomId:       r.ParentRoomID,
				CreationTime:       r.CreationTime,
			},
		}

		participants, err := m.lk.LoadParticipants(r.RoomId)
		if err == nil {
			i.ParticipantsInfo = participants
		}

		rri, err := m.lk.LoadRoomInfo(r.RoomId)
		if err == nil {
			i.RoomInfo.Metadata = rri.Metadata
		}

		res = append(res, i)
	}

	return true, "success", res
}

func (m *RoomModel) FetchPastRooms(r *plugnmeet.FetchPastRoomsReq) (*plugnmeet.FetchPastRoomsResult, error) {
	rooms, total, err := m.ds.GetPastRooms(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}
	var list []*plugnmeet.PastRoomInfo

	for _, rr := range rooms {
		room := &plugnmeet.PastRoomInfo{
			RoomTitle:          rr.RoomTitle,
			RoomId:             rr.RoomId,
			RoomSid:            rr.Sid,
			JoinedParticipants: rr.JoinedParticipants,
			WebhookUrl:         rr.WebhookUrl,
			Created:            rr.Created.Format("2006-01-02 15:04:05"),
			Ended:              rr.Ended.Format("2006-01-02 15:04:05"),
		}
		if an, err := m.ds.GetAnalyticByRoomTableId(rr.ID); err == nil && an != nil {
			room.AnalyticsFileId = an.FileID
		}
		list = append(list, room)
	}

	result := &plugnmeet.FetchPastRoomsResult{
		TotalRooms: total,
		From:       r.From,
		Limit:      r.Limit,
		OrderBy:    r.OrderBy,
		RoomsList:  list,
	}

	return result, nil
}
