package models

import (
	"encoding/json"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    r.GetRoomId(),
		"userId":    r.GetUserId(),
		"blockUser": r.GetBlockUser(),
		"method":    "RemoveParticipant",
	})
	log.Infoln("New request to remove participant received")

	status, err := m.natsService.GetRoomUserStatus(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room user status")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}

	if status != natsservice.UserStatusOnline {
		log.Warnln("user not online")
		return plugnmeet.StatusCode_USER_NOT_FOUND, config.ErrUserNotOnline
	}

	if err := m.natsService.NotifyErrorMsg(r.RoomId, r.Msg, &r.UserId); err != nil {
		log.WithError(err).Errorln("error notifying user with custom message")
	}

	if r.Msg == "" {
		r.Msg = "notifications.room-disconnected-participant-removed"
	}

	// send notification to be disconnected
	if err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), r.Msg, &r.UserId); err != nil {
		log.WithError(err).Errorln("error broadcasting SESSION_ENDED event")
	}

	if err = m.natsService.UpdateUserStatus(r.RoomId, r.UserId, natsservice.UserStatusRemoved); err != nil {
		log.WithError(err).Warnln("error updating user status to removed")
	}

	// now remove from lk
	if _, err = m.lk.RemoveParticipant(r.RoomId, r.UserId); err != nil {
		log.WithError(err).Errorln("error removing user from livekit, keep continuing")
	}

	// finally, check if requested to block as well as
	if r.BlockUser {
		log.Infoln("blocking user")
		if err := m.natsService.AddUserToBlockList(r.RoomId, r.UserId); err != nil {
			log.WithError(err).Errorln("error adding user to block list")
		}
	}

	// clean up the consumer & drop the NATS connection after the session ended event is delivered
	roomId := r.GetRoomId()
	userId := r.GetUserId()
	time.AfterFunc(time.Second, func() {
		m.natsService.DeleteConsumer(roomId, userId)
		m.natsService.KickUserConnection(roomId, userId)
	})

	log.Infoln("Participant removed successfully")
	return plugnmeet.StatusCode_SUCCESS, nil
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
	metadata.RaisedHand.IsRaised = true
	metadata.RaisedHand.RaisedAt = time.Now().UnixMilli()
	if err := m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil); err != nil {
		log.WithError(err).Errorln("error updating user metadata")
	}

	if metadata.RaisedHand.IsRaised {
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

	// when raised inside a breakout room, admins in the main room or in other
	// breakout rooms cannot see it — fan the notice out to them as well.
	m.notifyAdminsOutsideBreakoutRoom(roomId, userId)
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
	metadata.RaisedHand.IsRaised = false
	metadata.RaisedHand.RaisedAt = 0
	if err := m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, metadata, nil); err != nil {
		log.WithError(err).Errorln("error updating user metadata")
	}
}

// LowerOtherUserHand lowers the hand of another participant; only a room admin may do that.
func (m *UserModel) LowerOtherUserHand(roomId, requesterUserId, targetUserId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":       roomId,
		"userId":       requesterUserId,
		"targetUserId": targetUserId,
		"method":       "LowerOtherUserHand",
	})

	metadata, err := m.natsService.GetUserMetadataStruct(roomId, requesterUserId)
	if err != nil {
		log.WithError(err).Errorln("error getting user metadata")
	}
	if metadata == nil || !metadata.IsAdmin {
		log.Warnln("requester is not an admin, cannot lower another user's hand")
		return
	}

	m.LowerHand(roomId, targetUserId)
}

// notifyAdminsOutsideBreakoutRoom fans a raised-hand notice out to admins who
// are NOT in the given breakout room: when a participant raises their hand
// inside a breakout room, the in-room admin loop in RaisedHand cannot reach
// admins sitting in the main room or in other breakout rooms. The notice is
// sent as an i18n key plus interpolation values (JSON) so each admin's client
// renders it in the admin's own locale, stating who raised their hand and in
// which room.
func (m *UserModel) notifyAdminsOutsideBreakoutRoom(roomId, userId string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"method": "notifyAdminsOutsideBreakoutRoom",
	})

	metadata, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Errorln("error getting room metadata")
		return
	}
	if metadata == nil {
		return
	}

	// a normal main-room raise is already covered by the in-room admin loop
	if !metadata.IsBreakoutRoom || metadata.ParentRoomId == "" {
		return
	}

	// metadata stores "<parent roomId>:<parent table id>"; only the plain room id can be used as a room key.
	parentRoomId, _, err := ParseBreakoutParentRoomRef(metadata.ParentRoomId)
	if err != nil {
		log.WithError(err).Warnln("failed to parse parent room reference, skipping breakout raise-hand notice")
		return
	}
	roomTitle := metadata.RoomTitle

	// resolve the raiser's display name; fall back to userId if unavailable.
	name := userId
	if p, _, gErr := m.natsService.GetUserWithMetadata(roomId, userId); gErr == nil && p != nil && p.Name != "" {
		name = p.Name
	} else if gErr != nil {
		log.WithError(gErr).Warn("failed to get user metadata for raise-hand notice; using userId")
	}

	room := roomTitle
	if room == "" {
		room = roomId
	}

	notice, jErr := json.Marshal(map[string]string{
		"key":  "notifications.breakout-room-raised-hand",
		"name": name,
		"room": room,
	})
	if jErr != nil {
		log.WithError(jErr).Errorln("error marshaling raise-hand notice")
		return
	}

	// scan the parent room plus every sibling breakout room EXCEPT the one the
	// hand was raised in (the in-room loop already handled that one).
	rooms := []string{parentRoomId}
	breakoutRooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(parentRoomId)
	if err != nil {
		log.WithError(err).Errorln("error getting breakout rooms")
	} else {
		for _, r := range breakoutRooms {
			room := new(plugnmeet.BreakoutRoom)
			if uErr := protojson.Unmarshal([]byte(r), room); uErr != nil {
				log.WithError(uErr).Warn("failed to unmarshal breakout room, skipping")
				continue
			}
			if room.Id == "" || room.Id == roomId {
				continue
			}
			rooms = append(rooms, room.Id)
		}
	}

	for _, rId := range rooms {
		participants, _ := m.natsService.GetOnlineUsersList(rId)
		for _, participant := range participants {
			if participant.IsAdmin && participant.UserId != userId {
				if nErr := m.natsService.NotifyInfoMsg(rId, string(notice), true, &participant.UserId); nErr != nil {
					log.WithError(nErr).WithField("target_admin_id", participant.UserId).Errorln("error notifying admin")
				}
			}
		}
	}
}
