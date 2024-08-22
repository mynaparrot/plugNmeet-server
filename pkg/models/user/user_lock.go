package usermodel

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func (m *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	if r.UserId == "all" {
		err := m.updateLockSettingsAllUsers(r)
		return err
	}

	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	um := updateParticipantLockMetadata{
		participantInfo: p,
		roomId:          r.RoomId,
		service:         r.Service,
		direction:       r.Direction,
	}
	err = m.updateParticipantLockMetadata(um)

	return err
}

func (m *UserModel) updateLockSettingsAllUsers(r *plugnmeet.UpdateUserLockSettingsReq) error {
	participants, err := m.lk.LoadParticipants(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		if r.RequestedUserId != p.Identity {
			um := updateParticipantLockMetadata{
				participantInfo: p,
				roomId:          r.RoomId,
				service:         r.Service,
				direction:       r.Direction,
			}
			_ = m.updateParticipantLockMetadata(um)
		}
	}

	// now we'll require updating room settings
	// so that future users can be applied same lock settings
	info, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(info.Metadata))
	copy(meta, info.Metadata)

	mt, _ := m.natsService.UnmarshalRoomMetadata(string(meta))

	l := m.changeLockSettingsMetadata(r.Service, r.Direction, mt.DefaultLockSettings)
	mt.DefaultLockSettings = l

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, m)

	return err
}

type updateParticipantLockMetadata struct {
	participantInfo *livekit.ParticipantInfo
	roomId          string
	service         string
	direction       string
}

func (m *UserModel) updateParticipantLockMetadata(um updateParticipantLockMetadata) error {
	if um.participantInfo.State == livekit.ParticipantInfo_ACTIVE {
		meta := make([]byte, len(um.participantInfo.Metadata))
		copy(meta, um.participantInfo.Metadata)

		mt, _ := m.natsService.UnmarshalUserMetadata(string(meta))
		l := m.changeLockSettingsMetadata(um.service, um.direction, mt.LockSettings)
		mt.LockSettings = l

		err := m.natsService.UpdateAndBroadcastUserMetadata(um.roomId, um.participantInfo.Identity, m, nil)
		return err
	}

	return errors.New(config.UserNotActive)
}

func (m *UserModel) changeLockSettingsMetadata(service string, direction string, l *plugnmeet.LockSettings) *plugnmeet.LockSettings {
	lock := new(bool)
	if direction == "lock" {
		*lock = true
	}

	switch service {
	case "mic":
		l.LockMicrophone = lock
	case "webcam":
		l.LockWebcam = lock
	case "screenShare":
		l.LockScreenSharing = lock
	case "chat":
		l.LockChat = lock
	case "sendChatMsg":
		l.LockChatSendMessage = lock
	case "chatFile":
		l.LockChatFileShare = lock
	case "privateChat":
		l.LockPrivateChat = lock
	case "whiteboard":
		l.LockWhiteboard = lock
	case "sharedNotepad":
		l.LockSharedNotepad = lock
	}

	return l
}
