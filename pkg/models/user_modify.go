package models

import (
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) error {
	status, err := m.natsService.GetRoomUserStatus(r.RoomId, r.UserId)
	if err != nil {
		m.logger.Errorln(fmt.Sprintf("error GetRoomUserStatus roomId %s; userId: %s; msg: %s", r.GetRoomId(), r.GetUserId(), err))
		return err
	}

	if status != natsservice.UserStatusOnline {
		return errors.New(config.UserNotActive)
	}

	err = m.natsService.NotifyErrorMsg(r.RoomId, r.Msg, &r.UserId)
	if err != nil {
		m.logger.WithError(err).Errorln("error notify error message")
	}

	// send notification to be disconnected
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), "notifications.room-disconnected-participant-removed", &r.UserId)
	if err != nil {
		m.logger.Errorln(fmt.Sprintf("error broadcasting SESSION_ENDED event roomId %s; userId: %s; msg: %s", r.GetRoomId(), r.GetUserId(), err))
	}

	// now remove from lk
	_, err = m.lk.RemoveParticipant(r.RoomId, r.UserId)
	if err != nil {
		m.logger.Errorln(fmt.Sprintf("error removing user from lk roomId %s; userId: %s; msg: %s", r.GetRoomId(), r.GetUserId(), err))
	}

	// finally, check if requested to block as well as
	if r.BlockUser {
		_, err = m.natsService.AddUserToBlockList(r.RoomId, r.UserId)
		if err != nil {
			m.logger.Errorln(fmt.Sprintf("error AddUserToBlockList roomId %s; userId: %s; msg: %s", r.GetRoomId(), r.GetUserId(), err))
		}
	}

	return nil
}

func (m *UserModel) RaisedHand(roomId, userId, msg string) {
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		m.logger.WithError(err).Errorln("error getting user metadata")
	}

	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = true
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		m.logger.WithError(err).Errorln("error updating user metadata")
	}

	if metadata.RaisedHand {
		analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs, m.logger.Logger)
		analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
			EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
			EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_RAISE_HAND,
			RoomId:    roomId,
			UserId:    &userId,
		})
	}

	// notify to admin
	participants, _ := m.natsService.GetOnlineUsersList(roomId)
	for _, participant := range participants {
		if participant.IsAdmin && userId != participant.UserId {
			err := m.natsService.NotifyInfoMsg(roomId, msg, true, &participant.UserId)
			if err != nil {
				m.logger.WithError(err).Errorln("error notify info message")
			}
		}
	}
}

func (m *UserModel) LowerHand(roomId, userId string) {
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		m.logger.WithError(err).Errorln("error getting user metadata")
	}
	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = false
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		m.logger.WithError(err).Errorln("error updating user metadata")
	}
}
