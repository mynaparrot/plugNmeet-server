package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func (m *RoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"userId": r.UserId,
		"method": "ApproveWaitingUsers",
	})

	if r.UserId == "all" {
		participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
		if err != nil {
			log.WithError(err).Errorln("failed to get online users list")
			return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
		}

		for _, p := range participants {
			mt, err := m.natsService.UnmarshalUserMetadata(p.Metadata)
			if err != nil || mt == nil || !mt.WaitForApproval {
				continue
			}
			if err = m.approveUser(r.RoomId, p.UserId, p.Metadata); err != nil {
				m.logger.WithError(err).Errorln("error approving user")
			}
		}

		return plugnmeet.StatusCode_SUCCESS, nil
	}

	p, err := m.natsService.GetUserInfo(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("failed to get user info")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	if p == nil {
		log.Warnln("user not found to approve")
		return plugnmeet.StatusCode_USER_NOT_FOUND, config.ErrRoomUserNotFound
	}

	if err = m.approveUser(r.RoomId, r.UserId, p.Metadata); err != nil {
		log.WithError(err).Errorln("failed to approve user")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}

func (m *RoomModel) approveUser(roomId, userId, metadata string) error {
	mt, err := m.natsService.UnmarshalUserMetadata(metadata)
	if err != nil {
		return err
	}
	mt.WaitForApproval = false // this mean doesn't need to wait anymore

	if err := m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil); err != nil {
		return err
	}

	// send the full room metadata before the user metadata, so on the user's
	// ordered consumer ROOM_METADATA_UPDATE arrives before USER_METADATA_UPDATE
	// (waitForApproval=false triggers the client to request the media server data)
	_ = m.natsService.BroadcastRoomMetadata(roomId, nil, &userId)
	// the waiting user's consumer only listens to the private subject, so notify it directly
	_ = m.natsService.BroadcastUserMetadata(roomId, userId, nil, &userId)

	return nil
}

func (m *RoomModel) UpdateWaitingRoomMessage(r *plugnmeet.UpdateWaitingRoomMessageReq) (plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": r.RoomId,
		"method": "UpdateWaitingRoomMessage",
	})

	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	if roomMeta == nil {
		log.Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}
	roomMeta.RoomFeatures.WaitingRoomFeatures.WaitingRoomMsg = r.Msg

	if err := m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}

	// waiting users only listen to the private subject, so notify them directly.
	// they only get the redacted metadata, prepared once for all of them.
	participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
	if err != nil {
		return plugnmeet.StatusCode_SUCCESS, nil
	}
	redactedMeta, err := m.natsService.RedactRoomMetadataForWaitingUser(roomMeta)
	if err != nil {
		log.WithError(err).Errorln("failed to redact room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrRoomUnexpectedError
	}
	for _, p := range participants {
		mt, err := m.natsService.UnmarshalUserMetadata(p.Metadata)
		if err != nil || mt == nil || !mt.WaitForApproval {
			continue
		}
		_ = m.natsService.BroadcastRoomMetadata(r.RoomId, &redactedMeta, new(p.UserId))
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}
