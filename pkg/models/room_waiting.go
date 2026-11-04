package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func (m *RoomModel) ApproveWaitingUsers(r *plugnmeet.ApproveWaitingUsersReq) error {
	if r.UserId == "all" {
		participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
		if err != nil {
			return err
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

		return nil
	}

	p, err := m.natsService.GetUserInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("user not found")
	}

	return m.approveUser(r.RoomId, r.UserId, p.Metadata)
}

func (m *RoomModel) approveUser(roomId, userId, metadata string) error {
	mt, err := m.natsService.UnmarshalUserMetadata(metadata)
	if err != nil {
		return err
	}
	mt.WaitForApproval = false // this mean doesn't need to wait anymore

	if err := m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil); err != nil {
		return fmt.Errorf("can't approve user. try again")
	}

	// send the full room metadata before the user metadata, so on the user's
	// ordered consumer ROOM_METADATA_UPDATE arrives before USER_METADATA_UPDATE
	// (waitForApproval=false triggers the client to request the media server data)
	_ = m.natsService.BroadcastRoomMetadata(roomId, nil, &userId)
	// the waiting user's consumer only listens to the private subject, so notify it directly
	_ = m.natsService.BroadcastUserMetadata(roomId, userId, nil, &userId)

	return nil
}

func (m *RoomModel) UpdateWaitingRoomMessage(r *plugnmeet.UpdateWaitingRoomMessageReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	if roomMeta == nil {
		return config.InvalidNilRoomMetadata
	}
	roomMeta.RoomFeatures.WaitingRoomFeatures.WaitingRoomMsg = r.Msg

	if err := m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		return err
	}

	// waiting users only listen to the private subject, so notify them directly.
	// they only get the redacted metadata, prepared once for all of them.
	participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
	if err != nil {
		return nil
	}
	redactedMeta, err := m.natsService.RedactRoomMetadataForWaitingUser(roomMeta)
	if err != nil {
		return err
	}
	for _, p := range participants {
		mt, err := m.natsService.UnmarshalUserMetadata(p.Metadata)
		if err != nil || mt == nil || !mt.WaitForApproval {
			continue
		}
		_ = m.natsService.BroadcastRoomMetadata(r.RoomId, &redactedMeta, new(p.UserId))
	}

	return nil
}
