package models

import (
	"context"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
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

	res := m.buildActiveRoomWithParticipant(ctx, roomDbInfo, rrr)

	// an active parent room reports its running breakout rooms, and its own
	// participant count spans all of them, so online learners in 1-on-1 AI
	// sessions show up right away instead of after the next janitor pass.
	if roomDbInfo.IsBreakoutRoom == 0 {
		parentRef := helpers.FormatBreakoutParentRoomRef(roomDbInfo.RoomId, roomDbInfo.ID)
		childRows, cErr := m.ds.GetActiveBreakoutRoomsByParentRef(ctx, parentRef)
		if cErr != nil {
			log.WithError(cErr).Errorln("failed to get active breakout rooms")
		}
		for i := range childRows {
			childDbInfo := &childRows[i]
			childNatsInfo, cErr := m.natsService.GetRoomInfo(childDbInfo.RoomId)
			if cErr != nil || childNatsInfo == nil || !m.natsService.IsRoomStatusActive(childNatsInfo.Status) {
				continue
			}
			res.BreakoutRooms = append(res.BreakoutRooms, m.buildActiveRoomWithParticipant(ctx, childDbInfo, childNatsInfo))
		}
		if live := countJoinedHumanParticipants(roomWithBreakouts(res)...); live > 0 {
			res.RoomInfo.JoinedParticipants = live
		}
	}

	return true, config.MsgSuccess, plugnmeet.StatusCode_SUCCESS, res
}

// newActiveRoomInfo builds the reported info of a room from its DB row and its
// NATS state.
func newActiveRoomInfo(room *dbmodels.RoomInfo, rInfo *plugnmeet.NatsKvRoomInfo) *plugnmeet.ActiveRoomInfo {
	return &plugnmeet.ActiveRoomInfo{
		RoomTitle:          room.RoomTitle,
		RoomId:             room.RoomId,
		Sid:                room.Sid,
		JoinedParticipants: room.JoinedParticipants,
		IsRunning:          int32(room.IsRunning),
		IsRecording:        int32(room.IsRecording),
		IsActiveRtmp:       int32(room.IsActiveRtmp),
		WebhookUrl:         room.WebhookUrl,
		IsBreakoutRoom:     int32(room.IsBreakoutRoom),
		ParentRoomId:       helpers.PlainParentRoomIdFromRef(room.ParentRoomID),
		CreationTime:       room.CreationTime,
		Metadata:           rInfo.GetMetadata(),
	}
}

// buildActiveRoomWithParticipant assembles a room with its live participants.
// Internal system users (ingress, sip, agent bots) are not participants, so they
// are left out of the list and of the count.
func (m *RoomModel) buildActiveRoomWithParticipant(ctx context.Context, room *dbmodels.RoomInfo, rInfo *plugnmeet.NatsKvRoomInfo) *plugnmeet.ActiveRoomWithParticipant {
	res := &plugnmeet.ActiveRoomWithParticipant{RoomInfo: newActiveRoomInfo(room, rInfo)}

	if participants, err := m.lk.LoadParticipants(ctx, room.RoomId); err == nil && len(participants) > 0 {
		for _, participant := range participants {
			if participant == nil || config.IsUserIdInternal(participant.Identity) {
				continue
			}
			entry, err := m.natsService.GetUserKeyValue(room.RoomId, participant.Identity, natsservice.UserMetadataKey)
			if err != nil || entry == nil {
				continue
			}
			participant.Metadata = string(entry.Value())
			res.ParticipantsInfo = append(res.ParticipantsInfo, participant)
		}
	}

	// the live count is only trusted when somebody is actually online, so a room
	// that is still being filled keeps the value the janitor wrote
	if live := countJoinedHumanParticipants(res); live > 0 {
		res.RoomInfo.JoinedParticipants = live
	}

	return res
}

// countJoinedHumanParticipants returns the deduplicated number of human
// participants across the given rooms.
func countJoinedHumanParticipants(rooms ...*plugnmeet.ActiveRoomWithParticipant) int64 {
	seen := make(map[string]struct{})
	for _, room := range rooms {
		for _, participant := range room.GetParticipantsInfo() {
			identity := participant.GetIdentity()
			if identity == "" || config.IsUserIdInternal(identity) {
				continue
			}
			seen[identity] = struct{}{}
		}
	}
	return int64(len(seen))
}

// roomWithBreakouts returns the room followed by its breakout rooms.
func roomWithBreakouts(room *plugnmeet.ActiveRoomWithParticipant) []*plugnmeet.ActiveRoomWithParticipant {
	return append([]*plugnmeet.ActiveRoomWithParticipant{room}, room.GetBreakoutRooms()...)
}

// childRoom keeps a breakout room together with the parent reference it was
// stored with, so it can be attached to that exact parent row.
type childRoom struct {
	parentRef string
	room      *plugnmeet.ActiveRoomWithParticipant
}

func (m *RoomModel) GetActiveRoomsInfo(userCtx context.Context) (bool, string, plugnmeet.StatusCode, []*plugnmeet.ActiveRoomWithParticipant) {
	roomsInfo, err := m.ds.GetActiveRoomsInfo(userCtx)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to get active rooms info from db")
		return false, config.ErrRoomUnexpectedError.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, nil
	}
	if roomsInfo == nil || len(roomsInfo) == 0 {
		return false, config.ErrNoActiveRoomFound.Error(), plugnmeet.StatusCode_NOT_FOUND, nil
	}
	rooms := make([]*plugnmeet.ActiveRoomWithParticipant, 0, len(roomsInfo))
	children := make([]childRoom, 0)
	// parentsByRef is keyed by the reference stored in the children's
	// ParentRoomID, so a child only attaches to the exact parent row it was
	// created for, even if the room id has been reused since
	parentsByRef := make(map[string]*plugnmeet.ActiveRoomWithParticipant, len(roomsInfo))

	for i := range roomsInfo {
		r := &roomsInfo[i]
		rri, err := m.natsService.GetRoomInfo(r.RoomId)
		if err != nil || rri == nil {
			continue
		}

		room := m.buildActiveRoomWithParticipant(userCtx, r, rri)
		if r.IsBreakoutRoom == 1 && r.ParentRoomID != "" {
			children = append(children, childRoom{parentRef: r.ParentRoomID, room: room})
			continue
		}
		rooms = append(rooms, room)
		parentsByRef[helpers.FormatBreakoutParentRoomRef(r.RoomId, r.ID)] = room
	}

	// a breakout room belongs to its parent, so it is reported there instead of
	// as a room of its own
	for _, child := range children {
		parent, ok := parentsByRef[child.parentRef]
		if !ok {
			// keep a running room whose parent is gone instead of dropping it
			rooms = append(rooms, child.room)
			continue
		}
		parent.BreakoutRooms = append(parent.BreakoutRooms, child.room)
	}

	// a parent room is occupied as long as anybody is in it or in a breakout
	for _, room := range rooms {
		if len(room.GetBreakoutRooms()) == 0 {
			continue
		}
		if live := countJoinedHumanParticipants(roomWithBreakouts(room)...); live > 0 {
			room.RoomInfo.JoinedParticipants = live
		}
	}

	return true, config.MsgSuccess, plugnmeet.StatusCode_SUCCESS, rooms
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
		return plugnmeet.StatusCode_NOT_FOUND, nil, config.ErrNoRoomFound
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
