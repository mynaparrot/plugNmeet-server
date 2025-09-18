package models

import (
	"context"
	"errors"

	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *BreakoutRoomModel) EndBreakoutRoom(ctx context.Context, r *plugnmeet.EndBreakoutRoomReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId":   r.RoomId,
		"breakoutRoomId": r.BreakoutRoomId,
		"method":         "EndBreakoutRoom",
	})
	log.Infoln("request to end a single breakout room")

	rm, err := m.natsService.GetBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get breakout room from nats")
		return err
	}
	if rm == nil {
		err = errors.New("breakout room not found")
		log.WithError(err).Warn()
		return err
	}
	m.proceedToEndBkRoom(ctx, r.BreakoutRoomId, r.RoomId, log)
	return nil
}

func (m *BreakoutRoomModel) EndAllBreakoutRoomsByParentRoomId(ctx context.Context, parentRoomId string) error {
	log := m.logger.WithFields(logrus.Fields{
		"parentRoomId": parentRoomId,
		"method":       "EndAllBreakoutRoomsByParentRoomId",
	})
	log.Infoln("request to end all breakout rooms")

	ids, err := m.natsService.GetBreakoutRoomIdsByParentRoomId(parentRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get breakout room ids from nats")
		return err
	}

	if ids == nil || len(ids) == 0 {
		log.Info("no active breakout rooms found to end")
		return m.updateParentRoomMetadata(parentRoomId, log)
	}

	for _, i := range ids {
		m.proceedToEndBkRoom(ctx, i, parentRoomId, log)
	}
	return nil
}

func (m *BreakoutRoomModel) proceedToEndBkRoom(ctx context.Context, bkRoomId, parentRoomId string, log *logrus.Entry) {
	roomLog := log.WithField("breakoutRoomId", bkRoomId)
	roomLog.Info("proceeding to end breakout room")

	ok, msg := m.rm.EndRoom(ctx, &plugnmeet.RoomEndReq{RoomId: bkRoomId})
	if !ok {
		roomLog.WithField("endRoomMsg", msg).Error("failed to end breakout room via room model")
	}

	err := m.natsService.DeleteBreakoutRoom(parentRoomId, bkRoomId)
	if err != nil {
		roomLog.WithError(err).Error("failed to delete breakout room from nats")
	}

	m.onAfterBkRoomEnded(parentRoomId, bkRoomId, roomLog)
}

func (m *BreakoutRoomModel) onAfterBkRoomEnded(parentRoomId, bkRoomId string, log *logrus.Entry) {
	log.Info("performing post-end tasks for breakout room")
	if c, err := m.natsService.CountBreakoutRooms(parentRoomId); err == nil && c == 0 {
		log.Info("last breakout room ended, cleaning up parent room metadata")
		// no room left so, delete breakoutRoomKey key for this room
		m.natsService.DeleteAllBreakoutRoomsByParentRoomId(parentRoomId)
		_ = m.updateParentRoomMetadata(parentRoomId, log)
	}
	// notify to the room for updating list
	if err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_BREAKOUT_ROOM_ENDED, parentRoomId, bkRoomId, nil); err != nil {
		log.WithError(err).Error("failed to broadcast BREAKOUT_ROOM_ENDED event")
	}
}

func (m *BreakoutRoomModel) updateParentRoomMetadata(parentRoomId string, log *logrus.Entry) error {
	log.Info("updating parent room metadata to disable breakout room features")
	// if no rooms left, then we can update metadata
	meta, err := m.natsService.GetRoomMetadataStruct(parentRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get parent room metadata")
		return err
	}
	if meta == nil {
		log.Warn("parent room metadata not found, likely room already ended")
		return nil
	}

	if !meta.RoomFeatures.BreakoutRoomFeatures.IsActive {
		log.Info("breakout room feature is already inactive in parent room metadata")
		return nil
	}

	meta.RoomFeatures.BreakoutRoomFeatures.IsActive = false
	err = m.natsService.UpdateAndBroadcastRoomMetadata(parentRoomId, meta)
	if err != nil {
		log.WithError(err).Error("failed to update and broadcast parent room metadata")
		return err
	}

	log.Info("successfully updated parent room metadata")
	return nil
}

func (m *BreakoutRoomModel) PostTaskAfterRoomEndWebhook(ctx context.Context, roomId, metadata string) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"method": "PostTaskAfterRoomEndWebhook",
	})

	if metadata == "" {
		return nil
	}
	meta, err := m.natsService.UnmarshalRoomMetadata(metadata)
	if err != nil {
		log.WithError(err).Warn("could not unmarshal room metadata, skipping breakout room webhook tasks")
		return err
	}

	if meta.IsBreakoutRoom {
		log.Info("breakout room ended, cleaning up its records")
		_ = m.natsService.DeleteBreakoutRoom(meta.ParentRoomId, roomId)
		m.onAfterBkRoomEnded(meta.ParentRoomId, roomId, log)
	} else {
		log.Info("parent room ended, ending all associated breakout rooms")
		err = m.EndAllBreakoutRoomsByParentRoomId(ctx, roomId)
		if err != nil {
			log.WithError(err).Error("failed to end all breakout rooms")
			return err
		}
	}

	return nil
}
