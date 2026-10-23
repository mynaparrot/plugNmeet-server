package models

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"google.golang.org/protobuf/encoding/protojson"
)

// breakoutParentRoomRefFormat is the persisted form of
// dbmodels.RoomInfo.ParentRoomID for breakout rooms. It stores both the parent
// room id and the parent's DB table id so a parent row can be resolved exactly,
// even when the same roomId has multiple sessions in room_info.
const breakoutParentRoomRefFormat = "%s:%d"

type BreakoutRoomModel struct {
	rs             *redisservice.RedisService
	natsService    *natsservice.NatsService
	rm             *RoomModel
	analyticsModel *AnalyticsModel
	um             *UserModel
	logger         *logrus.Entry
}

type BreakoutRoomModelArgs struct {
	fx.In
	Rs             *redisservice.RedisService
	NatsService    *natsservice.NatsService
	Rm             *RoomModel
	AnalyticsModel *AnalyticsModel
	UserModel      *UserModel
	Logger         *logrus.Logger
}

func NewBreakoutRoomModel(args BreakoutRoomModelArgs) *BreakoutRoomModel {
	return &BreakoutRoomModel{
		rs:             args.Rs,
		natsService:    args.NatsService,
		rm:             args.Rm,
		analyticsModel: args.AnalyticsModel,
		um:             args.UserModel,
		logger:         args.Logger.WithField("model", "breakout_room"),
	}
}

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *BreakoutRoomModel) SendBreakoutRoomMsg(r *plugnmeet.BroadcastBreakoutRoomMsgReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId": r.RoomId,
		"method":       "SendBreakoutRoomMsg",
	})
	log.Infoln("New request received to send message to all breakout rooms")

	rooms, err := m.fetchBreakoutRooms(r.RoomId)
	if err != nil {
		log.WithError(err).Error("Failed to fetch breakout rooms")
		return config.ErrBkRoomUnexpectedError
	}

	if rooms == nil || len(rooms) == 0 {
		log.Info("No active breakout rooms found to send message")
		return nil
	}

	for _, rr := range rooms {
		if err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG, rr.Id, r.Msg, nil); err != nil {
			log.WithError(err).WithField("breakoutRoomId", rr.Id).Error("Failed to broadcast message to breakout room")
		}
	}

	log.Info("Successfully broadcasted message to all breakout rooms")
	return nil
}

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"duration":       r.Duration,
		"method":         "IncreaseBreakoutRoomDuration",
	})
	log.Infoln("New request to increase breakout room duration received")

	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.Error("Failed to fetch breakout room info")
		return err
	}

	// update in a room duration checker
	log.Info("Increasing duration in room duration checker")
	newDuration, err := m.rm.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		log.WithError(err).Error("Failed to increase room duration")
		return err
	}

	// now update nats
	log.Info("Updating breakout room info in nats")
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("Failed to marshal breakout room data")
		return config.ErrBkRoomUnexpectedError
	}

	if err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, r.BreakoutRoomId, marshal); err != nil {
		log.WithError(err).Error("Failed to update breakout room in nats")
		return config.ErrBkRoomUnexpectedError
	}

	log.WithField("new_duration", newDuration).Info("Successfully increased breakout room duration")
	return nil
}

// FormatBreakoutParentRoomRef encodes a parent room reference for persistence
// in dbmodels.RoomInfo.ParentRoomID. "<parent roomId>:<parent table id>"
func FormatBreakoutParentRoomRef(parentRoomId string, parentTableId uint64) string {
	return fmt.Sprintf(breakoutParentRoomRefFormat, parentRoomId, parentTableId)
}

// isParentWithActiveBreakouts reports whether the given room metadata describes
// a parent (non-breakout) room whose breakout rooms are currently active.
// Such rooms must not be terminated by an empty-close/pause, because their
// breakout rooms are still running and govern the parent's lifecycle.
func isParentWithActiveBreakouts(meta *plugnmeet.RoomMetadata) bool {
	if meta == nil {
		return false
	}
	return !meta.IsBreakoutRoom &&
		meta.RoomFeatures != nil &&
		meta.RoomFeatures.BreakoutRoomFeatures != nil &&
		meta.RoomFeatures.BreakoutRoomFeatures.IsActive
}

// breakoutChildOfActiveParent reports whether meta belongs to a breakout child
// room whose parent still has active breakout rooms. Such rooms may
// legitimately sit empty (self-select) and must not be empty-closed or ended
// while the parent's breakout session is still running.
func breakoutChildOfActiveParent(ns *natsservice.NatsService, meta *plugnmeet.RoomMetadata) bool {
	if meta == nil || !meta.IsBreakoutRoom || meta.ParentRoomId == "" {
		return false
	}
	// metadata stores "<parent roomId>:<parent table id>"; only the plain room
	// id can be used as a parent lookup key.
	parentRoomId, _, err := ParseBreakoutParentRoomRef(meta.ParentRoomId)
	if err != nil {
		return false
	}
	parentMeta, err := ns.GetRoomMetadataStruct(parentRoomId)
	if err != nil || parentMeta == nil {
		return false
	}
	return isParentWithActiveBreakouts(parentMeta)
}

// ParseBreakoutParentRoomRef decodes a persisted parent room reference. A
// reference must contain a non-empty room id followed by a colon (:) and a positive
// uint64 table id e.g. "<parent roomId>:<parent table id>" ; malformed values are rejected.
func ParseBreakoutParentRoomRef(ref string) (string, uint64, error) {
	sep := strings.LastIndexByte(ref, ':')
	if sep <= 0 || sep == len(ref)-1 {
		return "", 0, fmt.Errorf("invalid parent room reference %q", ref)
	}

	parentTableId, err := strconv.ParseUint(ref[sep+1:], 10, 64)
	if err != nil || parentTableId == 0 {
		return "", 0, fmt.Errorf("invalid parent room reference %q", ref)
	}

	return ref[:sep], parentTableId, nil
}

// plainParentRoomIdFromRef returns only the room id part of a persisted parent
// room reference, for API responses that expose the plain parent room id.
func plainParentRoomIdFromRef(ref string) string {
	roomId, _, err := ParseBreakoutParentRoomRef(ref)
	if err != nil {
		return ""
	}
	return roomId
}
