package usermodel

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func (u *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	if r.UserId == "all" {
		err := u.updateLockSettingsAllUsers(r)
		return err
	}

	p, err := u.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	um := updateParticipantLockMetadata{
		participantInfo: p,
		roomId:          r.RoomId,
		service:         r.Service,
		direction:       r.Direction,
	}
	err = u.updateParticipantLockMetadata(um)

	return err
}

func (u *UserModel) updateLockSettingsAllUsers(r *plugnmeet.UpdateUserLockSettingsReq) error {
	participants, err := u.lk.LoadParticipants(r.RoomId)
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
			_ = u.updateParticipantLockMetadata(um)
		}
	}

	// now we'll require updating room settings
	// so that future users can be applied same lock settings
	info, err := u.lk.LoadRoomInfo(r.RoomId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(info.Metadata))
	copy(meta, info.Metadata)

	m, _ := u.lk.UnmarshalRoomMetadata(string(meta))

	l := u.changeLockSettingsMetadata(r.Service, r.Direction, m.DefaultLockSettings)
	m.DefaultLockSettings = l

	_, err = u.lk.UpdateRoomMetadataByStruct(r.RoomId, m)

	return err
}

type updateParticipantLockMetadata struct {
	participantInfo *livekit.ParticipantInfo
	roomId          string
	service         string
	direction       string
}

func (u *UserModel) updateParticipantLockMetadata(um updateParticipantLockMetadata) error {
	if um.participantInfo.State == livekit.ParticipantInfo_ACTIVE {
		meta := make([]byte, len(um.participantInfo.Metadata))
		copy(meta, um.participantInfo.Metadata)

		m, _ := u.lk.UnmarshalParticipantMetadata(string(meta))
		l := u.changeLockSettingsMetadata(um.service, um.direction, m.LockSettings)
		m.LockSettings = l

		_, err := u.lk.UpdateParticipantMetadataByStruct(um.roomId, um.participantInfo.Identity, m)
		return err
	}

	return errors.New(config.UserNotActive)
}

func (u *UserModel) changeLockSettingsMetadata(service string, direction string, l *plugnmeet.LockSettings) *plugnmeet.LockSettings {
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
