package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

// IsRoomActive checks if a room is active by using NATS as the primary source of truth.
// This is a high-performance, cache-aware function.
func (m *RoomModel) IsRoomActive(r *plugnmeet.IsRoomActiveReq) (*plugnmeet.IsRoomActiveRes, *plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata) {
	res := &plugnmeet.IsRoomActiveRes{
		Status:   true,
		IsActive: false,
		Msg:      "room is not active",
	}

	// NATS is the single source of truth for this check.
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		res.Status = false
		res.Msg = err.Error()
		return res, nil, nil
	}

	if rInfo == nil || meta == nil {
		// Room isn't active in NATS.
		return res, nil, nil
	}

	if rInfo.Status == natsservice.RoomStatusCreated || rInfo.Status == natsservice.RoomStatusActive {
		res.IsActive = true
		res.Msg = "room is active"
	}
	// If status is "ended" or anything else, it will correctly return IsActive: false and "room is not active".

	return res, rInfo, meta
}

func (m *RoomModel) GetActiveRoomInfo(ctx context.Context, r *plugnmeet.GetActiveRoomInfoReq) (bool, string, *plugnmeet.ActiveRoomWithParticipant) {
	log := m.logger.WithFields(logrus.Fields{"roomId": r.RoomId, "method": "GetActiveRoomInfo"})
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
	res := make([]*plugnmeet.ActiveRoomWithParticipant, 0, len(roomsInfo))

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
	// If the limit exceeds the maximum, cap it at the maximum.
	if r.Limit > 100 {
		r.Limit = 100
	}
	if r.OrderBy == "" {
		r.OrderBy = "DESC"
	}
	rooms, total, err := m.ds.GetPastRooms(r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		return nil, err
	}
	list := make([]*plugnmeet.PastRoomInfo, 0, len(rooms))

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
			room.AnalyticsFileId = &an.ArtifactId
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
