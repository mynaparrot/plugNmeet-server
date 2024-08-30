package models

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
)

func (m *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) error {
	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State != livekit.ParticipantInfo_ACTIVE {
		return errors.New(config.UserNotActive)
	}

	err = m.natsService.NotifyErrorMsg(r.RoomId, r.Msg, &p.Identity)
	if err != nil {
		log.Errorln(err)
	}

	// now remove
	_, err = m.lk.RemoveParticipant(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	// finally, check if requested to block as well as
	if r.BlockUser {
		_, _ = m.natsService.AddUserToBlockList(r.RoomId, r.UserId)
	}

	return nil
}

func (m *UserModel) RaisedHand(roomId, userId, msg string) {
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		log.Errorln(err)
	}

	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = true
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		log.Errorln(err)
	}

	if metadata.RaisedHand {
		analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs)
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
				log.Errorln(err)
			}
		}
	}
}

func (m *UserModel) LowerHand(roomId, userId string) {
	metadata, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		log.Errorln(err)
	}
	if metadata == nil {
		return
	}

	// now update user's metadata
	metadata.RaisedHand = false
	err = m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil)
	if err != nil {
		log.Errorln(err)
	}
}
