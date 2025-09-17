package models

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// AssignLockSettingsToUser will assign lock to no-admin user
// it will consider default room lock settings too
func (m *UserModel) AssignLockSettingsToUser(meta *plugnmeet.RoomMetadata, g *plugnmeet.GenerateTokenReq) {
	if g.UserInfo.UserMetadata.LockSettings == nil {
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)
	}
	l := new(plugnmeet.LockSettings)

	// if no lock settings were sent,
	// then we'll use default room lock settings
	dl := meta.DefaultLockSettings
	ul := g.UserInfo.UserMetadata.LockSettings

	if ul.LockWebcam == nil && dl.LockWebcam != nil {
		l.LockWebcam = dl.LockWebcam
	} else if ul.LockWebcam != nil {
		l.LockWebcam = ul.LockWebcam
	}

	if ul.LockMicrophone == nil && dl.LockMicrophone != nil {
		l.LockMicrophone = dl.LockMicrophone
	} else if ul.LockMicrophone != nil {
		l.LockMicrophone = ul.LockMicrophone
	}

	if ul.LockScreenSharing == nil && dl.LockScreenSharing != nil {
		l.LockScreenSharing = dl.LockScreenSharing
	} else if ul.LockScreenSharing != nil {
		l.LockScreenSharing = ul.LockScreenSharing
	}

	if ul.LockChat == nil && dl.LockChat != nil {
		l.LockChat = dl.LockChat
	} else if ul.LockChat != nil {
		l.LockChat = ul.LockChat
	}

	if ul.LockChatSendMessage == nil && dl.LockChatSendMessage != nil {
		l.LockChatSendMessage = dl.LockChatSendMessage
	} else if ul.LockChatSendMessage != nil {
		l.LockChatSendMessage = ul.LockChatSendMessage
	}

	if ul.LockChatFileShare == nil && dl.LockChatFileShare != nil {
		l.LockChatFileShare = dl.LockChatFileShare
	} else if ul.LockChatFileShare != nil {
		l.LockChatFileShare = ul.LockChatFileShare
	}

	if ul.LockPrivateChat == nil && dl.LockPrivateChat != nil {
		l.LockPrivateChat = dl.LockPrivateChat
	} else if ul.LockPrivateChat != nil {
		l.LockPrivateChat = ul.LockPrivateChat
	}

	if ul.LockWhiteboard == nil && dl.LockWhiteboard != nil {
		l.LockWhiteboard = dl.LockWhiteboard
	} else if ul.LockWhiteboard != nil {
		l.LockWhiteboard = ul.LockWhiteboard
	}

	if ul.LockSharedNotepad == nil && dl.LockSharedNotepad != nil {
		l.LockSharedNotepad = dl.LockSharedNotepad
	} else if ul.LockSharedNotepad != nil {
		l.LockSharedNotepad = ul.LockSharedNotepad
	}

	g.UserInfo.UserMetadata.LockSettings = l
}

// UpdateUserLockSettings will handle request to update user lock settings
// if user id was sent "all" then it will update all the users
func (m *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	if r.UserId == "all" {
		return m.updateRoomUsersLockSettings(r)
	}

	p, err := m.natsService.GetUserInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("user not found")
	}

	return m.updateUserLockMetadata(r.RoomId, r.UserId, r.Service, r.Direction, p.Metadata)
}

// updateRoomUsersLockSettings will update lock settings for all existing users
// and room default lock settings for future users
func (m *UserModel) updateRoomUsersLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	participants, err := m.natsService.GetOnlineUsersList(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		if p.IsAdmin {
			continue
		}
		err := m.updateUserLockMetadata(r.RoomId, p.UserId, r.Service, r.Direction, p.Metadata)
		if err != nil {
			m.logger.WithError(err).Errorln("error updating user lock settings")
		}
	}

	// now we'll require updating room settings
	// so that future users can be applied same lock settings
	mt, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	if mt == nil {
		return errors.New("invalid nil room metadata information")
	}

	m.assignNewLockSetting(r.Service, r.Direction, mt.DefaultLockSettings)
	return m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, mt)
}

func (m *UserModel) updateUserLockMetadata(roomId, userId, service, direction, metadata string) error {
	mt, err := m.natsService.UnmarshalUserMetadata(metadata)
	if err != nil {
		return err
	}
	m.assignNewLockSetting(service, direction, mt.LockSettings)
	return m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil)
}

func (m *UserModel) assignNewLockSetting(service string, direction string, l *plugnmeet.LockSettings) *plugnmeet.LockSettings {
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
