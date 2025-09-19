package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *RoomModel) IsRoomActive(ctx context.Context, r *plugnmeet.IsRoomActiveReq) (*plugnmeet.IsRoomActiveRes, *dbmodels.RoomInfo, *plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata) {
	log := m.logger.WithField("roomId", r.GetRoomId())
	// check first
	_ = waitUntilRoomCreationCompletes(ctx, m.rs, r.GetRoomId(), log)

	res := &plugnmeet.IsRoomActiveRes{
		Status: true,
		Msg:    "room is not active",
	}

	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if err != nil {
		res.Status = false
		res.Msg = err.Error()
		return res, nil, nil, nil
	}
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return res, nil, nil, nil
	}

	// let's make sure room actually active
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		res.Status = false
		res.Msg = err.Error()
		return res, nil, nil, nil
	}

	if rInfo == nil || meta == nil {
		// Room isn't active. Change status
		_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
			RoomId:    r.RoomId,
			IsRunning: 0,
		})
		return res, nil, nil, nil
	}

	if rInfo.Status == natsservice.RoomStatusCreated || rInfo.Status == natsservice.RoomStatusActive {
		res.IsActive = true
		res.Msg = "room is active"
	}

	return res, roomDbInfo, rInfo, meta
}

func (m *RoomModel) GetActiveRoomInfo(ctx context.Context, r *plugnmeet.GetActiveRoomInfoReq) (bool, string, *plugnmeet.ActiveRoomWithParticipant) {
	log := m.logger.WithField("roomId", r.GetRoomId())
	// check first
	_ = waitUntilRoomCreationCompletes(ctx, m.rs, r.GetRoomId(), log)

	roomDbInfo, _ := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "no room found", nil
	}

	rrr, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		return false, err.Error(), nil
	}
	if rrr == nil || (rrr.Status != natsservice.RoomStatusCreated && rrr.Status != natsservice.RoomStatusActive) {
		// The room is not in NATS or its status is not active, so we'll mark it as ended in the DB.
		log.WithField("nats_status", rrr.GetStatus()).Warn("room found in DB but not active in NATS, marking as ended")
		roomDbInfo.IsRunning = 0
		_, err := m.ds.UpdateRoomStatus(roomDbInfo)
		if err != nil {
			return false, err.Error(), nil
		}
		return false, "room is not active", nil
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

	if participants, err := m.lk.LoadParticipants(roomDbInfo.RoomId); err == nil && participants != nil && len(participants) > 0 {
		for _, participant := range participants {
			entry, err := m.natsService.GetUserKeyValue(roomDbInfo.RoomId, participant.Identity, natsservice.UserMetadataKey)
			if err != nil || entry == nil {
				continue
			}
			participant.Metadata = string(entry.Value())
			res.ParticipantsInfo = append(res.ParticipantsInfo, participant)
		}
	}

	return true, "success", res
}

func (m *RoomModel) GetActiveRoomsInfo() (bool, string, []*plugnmeet.ActiveRoomWithParticipant) {
	roomsInfo, err := m.ds.GetActiveRoomsInfo()
	if err != nil {
		return false, err.Error(), nil
	}
	if roomsInfo == nil || len(roomsInfo) == 0 {
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

		rri, err := m.natsService.GetRoomInfo(r.RoomId)
		if err != nil || rri == nil {
			continue
		}
		i.RoomInfo.Metadata = rri.Metadata

		if participants, err := m.lk.LoadParticipants(r.RoomId); err == nil && participants != nil && len(participants) > 0 {
			for _, participant := range participants {
				entry, err := m.natsService.GetUserKeyValue(r.RoomId, participant.Identity, natsservice.UserMetadataKey)
				if err != nil || entry == nil {
					continue
				}
				participant.Metadata = string(entry.Value())
				i.ParticipantsInfo = append(i.ParticipantsInfo, participant)
			}
		}

		res = append(res, i)
	}

	return true, "success", res
}

func (m *RoomModel) FetchPastRooms(r *plugnmeet.FetchPastRoomsReq) (*plugnmeet.FetchPastRoomsResult, error) {
	if r.Limit <= 0 {
		r.Limit = 20
	}
	if r.OrderBy == "" {
		r.OrderBy = "DESC"
	}
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
			Created:            rr.Created.Format(time.RFC3339),
			Ended:              rr.Ended.Format(time.RFC3339),
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
