package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    r.GetRoomId(),
		"userId":    r.GetUserId(),
		"blockUser": r.GetBlockUser(),
		"method":    "RemoveParticipant",
	})
	log.Infoln("request to remove participant")

	status, err := m.natsService.GetRoomUserStatus(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room user status")
		return err
	}

	if status != natsservice.UserStatusOnline {
		err = errors.New(config.UserNotActive)
		log.WithError(err).Warnln("user not online")
		return err
	}

	err = m.natsService.NotifyErrorMsg(r.RoomId, r.Msg, &r.UserId)
	if err != nil {
		log.WithError(err).Errorln("error notifying user with custom message")
	}

	// send notification to be disconnected
	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), "notifications.room-disconnected-participant-removed", &r.UserId)
	if err != nil {
		log.WithError(err).Errorln("error broadcasting SESSION_ENDED event")
	}

	// now remove from lk
	_, err = m.lk.RemoveParticipant(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("error removing user from livekit, keep continuing")
	}

	// finally, check if requested to block as well as
	if r.BlockUser {
		log.Infoln("blocking user")
		_, err = m.natsService.AddUserToBlockList(r.RoomId, r.UserId)
		if err != nil {
			log.WithError(err).Errorln("error adding user to block list")
		}
	}

	log.Infoln("participant removed successfully")
	return nil
}

func (m *UserModel) RaisedHand(roomId, userId, msg string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "RaisedHand",
	})
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		log.WithError(err).Errorln("error getting user metadata")
	}

	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = true
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		log.WithError(err).Errorln("error updating user metadata")
	}

	if metadata.RaisedHand {
		m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
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
				log.WithError(err).WithField("target_admin_id", participant.UserId).Errorln("error notifying admin")
			}
		}
	}
}

func (m *UserModel) LowerHand(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "LowerHand",
	})
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		log.WithError(err).Errorln("error getting user metadata")
	}
	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = false
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		log.WithError(err).Errorln("error updating user metadata")
	}
}
