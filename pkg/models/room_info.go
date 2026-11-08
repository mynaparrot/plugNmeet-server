package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

// IsRoomActive checks if a room is active by using NATS as the primary source of truth.
// This is a high-performance, cache-aware function.
func (m *RoomModel) IsRoomActive(r *plugnmeet.IsRoomActiveReq) (*plugnmeet.IsRoomActiveRes, *plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata) {
	res := &plugnmeet.IsRoomActiveRes{
		Status:     true,
		IsActive:   false,
		Msg:        config.ErrRoomNotActive.Error(),
		StatusCode: plugnmeet.StatusCode_ROOM_NOT_ACTIVE,
	}

	// NATS is the single source of truth for this check.
	rInfo, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		m.logger.WithField("roomId", r.RoomId).WithError(err).Errorln("failed to get room info with metadata")
		res.Status = false
		res.Msg = config.ErrRoomUnexpectedError.Error()
		res.StatusCode = plugnmeet.StatusCode_INTERNAL_SERVER_ERROR
		return res, nil, nil
	}

	if rInfo == nil || meta == nil {
		res.Msg = config.ErrRoomNotFound.Error()
		res.StatusCode = plugnmeet.StatusCode_ROOM_NOT_FOUND
		return res, nil, nil
	}

	if m.natsService.IsRoomStatusActive(rInfo.Status) {
		res.IsActive = true
		res.Msg = config.MsgSuccess
		res.StatusCode = plugnmeet.StatusCode_SUCCESS
	}
	// If status is "ended" or anything else, it will correctly return IsActive: false and "room is not active".

	return res, rInfo, meta
}

func (m *RoomModel) GetActiveRoomInfo(ctx context.Context, r *plugnmeet.GetActiveRoomInfoReq) (bool, string, plugnmeet.StatusCode, *plugnmeet.ActiveRoomWithParticipant) {
	log := m.logger.WithFields(logrus.Fields{"roomId": r.RoomId, "method": "GetActiveRoomInfo"})
	// check first
	_ = waitUntilRoomCreationCompletes(ctx, m.rs, r.GetRoomId(), log)

	roomDbInfo, _ := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, config.ErrRoomNotFound.Error(), plugnmeet.StatusCode_ROOM_NOT_FOUND, nil
	}

	rrr, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room info from NATS")
		return false, config.ErrRoomUnexpectedError.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil
	}
	if rrr == nil || (rrr.Status != natsservice.RoomStatusCreated && rrr.Status != natsservice.RoomStatusActive) {
		// The room is not in NATS or its status is not active, so we'll mark it as ended in the DB.
		log.WithField("nats_status", rrr.GetStatus()).Warn("room found in DB but not active in NATS, marking as ended")
		roomDbInfo.IsRunning = 0
		_, err := m.ds.UpdateRoomStatus(roomDbInfo)
		if err != nil {
			log.WithError(err).Errorln("failed to update room status in db")
			return false, config.ErrRoomUnexpectedError.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil
		}
		return false, config.ErrRoomNotActive.Error(), plugnmeet.StatusCode_ROOM_NOT_ACTIVE, nil
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
		ParentRoomId:       plainParentRoomIdFromRef(roomDbInfo.ParentRoomID),
		CreationTime:       roomDbInfo.CreationTime,
		Metadata:           rrr.Metadata,
	}

	if participants, err := m.lk.LoadParticipants(ctx, roomDbInfo.RoomId); err == nil && participants != nil && len(participants) > 0 {
		for _, participant := range participants {
			entry, err := m.natsService.GetUserKeyValue(roomDbInfo.RoomId, participant.Identity, natsservice.UserMetadataKey)
			if err != nil || entry == nil {
				continue
			}
			participant.Metadata = string(entry.Value())
			res.ParticipantsInfo = append(res.ParticipantsInfo, participant)
		}
	}

	return true, config.MsgSuccess, plugnmeet.StatusCode_SUCCESS, res
}

func (m *RoomModel) GetActiveRoomsInfo(userCtx context.Context) (bool, string, plugnmeet.StatusCode, []*plugnmeet.ActiveRoomWithParticipant) {
	roomsInfo, err := m.ds.GetActiveRoomsInfo(userCtx)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to get active rooms info from db")
		return false, config.ErrRoomUnexpectedError.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil
	}
	if roomsInfo == nil || len(roomsInfo) == 0 {
		return false, config.ErrRoomNotFound.Error(), plugnmeet.StatusCode_NOT_FOUND, nil
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
				ParentRoomId:       plainParentRoomIdFromRef(r.ParentRoomID),
				CreationTime:       r.CreationTime,
			},
		}

		rri, err := m.natsService.GetRoomInfo(r.RoomId)
		if err != nil || rri == nil {
			continue
		}
		i.RoomInfo.Metadata = rri.Metadata

		if participants, err := m.lk.LoadParticipants(userCtx, r.RoomId); err == nil && participants != nil && len(participants) > 0 {
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

	return true, config.MsgSuccess, plugnmeet.StatusCode_SUCCESS, res
}

func (m *RoomModel) FetchPastRooms(userCtx context.Context, r *plugnmeet.FetchPastRoomsReq) (plugnmeet.StatusCode, *plugnmeet.FetchPastRoomsResult, error) {
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
	rooms, total, err := m.ds.GetPastRooms(userCtx, r.RoomIds, uint64(r.From), uint64(r.Limit), &r.OrderBy)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to fetch past rooms from db")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil, config.ErrRoomUnexpectedError
	}
	if total == 0 {
		return plugnmeet.StatusCode_NOT_FOUND, nil, config.ErrRoomNotFound
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

	return plugnmeet.StatusCode_SUCCESS, result, nil
}
