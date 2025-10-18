package models

import (
	"errors"
	"reflect"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// AssignLockSettingsToUser will assign lock to a non-admin user.
func (m *UserModel) AssignLockSettingsToUser(meta *plugnmeet.RoomMetadata, g *plugnmeet.GenerateTokenReq) {
	if g.UserInfo.UserMetadata.LockSettings == nil {
		g.UserInfo.UserMetadata.LockSettings = new(plugnmeet.LockSettings)
	}
	if meta.DefaultLockSettings == nil {
		return
	}
	m.applyDefaultLockSettings(meta.DefaultLockSettings, g.UserInfo.UserMetadata.LockSettings)
}

// applyDefaultLockSettings uses reflection to merge default lock settings into a user's lock settings.
func (m *UserModel) applyDefaultLockSettings(defaultLocks, userLocks *plugnmeet.LockSettings) {
	defaultVal := reflect.ValueOf(defaultLocks).Elem()
	userVal := reflect.ValueOf(userLocks).Elem()

	for i := 0; i < userVal.NumField(); i++ {
		userField := userVal.Field(i)
		defaultField := defaultVal.Field(i)

		if userField.CanSet() && userField.Kind() == reflect.Ptr {
			if userField.IsNil() && !defaultField.IsNil() {
				userField.Set(defaultField)
			}
		}
	}
}

// UpdateUserLockSettings is the main entry point for updating lock settings.
// It now acts as a router for single-user or all-user updates.
func (m *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"method":    "UpdateUserLockSettings",
		"room_id":   r.RoomId,
		"service":   r.Service,
		"direction": r.Direction,
		"user_id":   r.UserId,
	})

	if r.UserId == "all" {
		// If "all", handle the batch update for the entire room.
		return m.handleUpdateAllUsersLockSettings(r, log)
	} else {
		// For a single user, perform the update and broadcast immediately.
		log.Info("request to update single user lock settings")
		err := m.updateAndBroadcastUserLock(r.RoomId, r.UserId, r.Service, r.Direction)
		if err != nil {
			log.WithError(err).Errorln("failed to update user lock settings")
		}
		return err
	}
}

// handleUpdateAllUsersLockSettings orchestrates the update for all users in a room.
func (m *UserModel) handleUpdateAllUsersLockSettings(r *plugnmeet.UpdateUserLockSettingsReq, log *logrus.Entry) error {
	log.Info("request to update all users lock settings")

	// First, update the room's default settings for any future users.
	err := m.updateDefaultRoomLockSettings(r)
	if err != nil {
		// Log the error but continue, as updating existing users is also important.
		log.WithError(err).Errorln("failed to update default room lock settings")
	}

	// Get the list of all online users to update them.
	userIds, err := m.natsService.GetOnlineUsersId(r.RoomId)
	if err != nil {
		return err
	}

	for _, id := range userIds {
		if id == r.RequestedUserId {
			// nothing for requested user
			continue
		}
		err = m.updateAndBroadcastUserLock(r.RoomId, id, r.Service, r.Direction)
		if err != nil {
			log.WithError(err).WithField("user_id", id).Errorln("failed to update user lock settings during all-user change")
		}
	}

	return nil
}

// updateAndBroadcastUserLock is the single, reusable worker function that updates
// a specific user's lock settings and broadcasts the change.
func (m *UserModel) updateAndBroadcastUserLock(roomId, userId, service, direction string) error {
	mt, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		return err
	}
	if mt == nil {
		return errors.New("user's metadata not found")
	}
	if mt.IsAdmin && service != "whiteboard" {
		// no lock for admin other than whiteboard
		return nil
	}

	// Apply the new setting to the struct.
	m.assignNewLockSetting(service, direction, mt.LockSettings)

	// Persist the change and notify the clients.
	return m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil)
}

// updateDefaultRoomLockSettings updates the room's default lock settings for future users.
func (m *UserModel) updateDefaultRoomLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	mt, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}
	if mt == nil {
		return errors.New("invalid nil room metadata information")
	}

	m.assignNewLockSetting(r.Service, r.Direction, mt.DefaultLockSettings)
	// Update the room metadata and broadcast the change.
	return m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, mt)
}

// lockSettingMap provides a map service strings to the
// corresponding fields in the LockSettings struct.
var lockSettingMap = map[string]func(l *plugnmeet.LockSettings, val *bool){
	"mic":           func(l *plugnmeet.LockSettings, val *bool) { l.LockMicrophone = val },
	"webcam":        func(l *plugnmeet.LockSettings, val *bool) { l.LockWebcam = val },
	"screenShare":   func(l *plugnmeet.LockSettings, val *bool) { l.LockScreenSharing = val },
	"chat":          func(l *plugnmeet.LockSettings, val *bool) { l.LockChat = val },
	"sendChatMsg":   func(l *plugnmeet.LockSettings, val *bool) { l.LockChatSendMessage = val },
	"chatFile":      func(l *plugnmeet.LockSettings, val *bool) { l.LockChatFileShare = val },
	"privateChat":   func(l *plugnmeet.LockSettings, val *bool) { l.LockPrivateChat = val },
	"whiteboard":    func(l *plugnmeet.LockSettings, val *bool) { l.LockWhiteboard = val },
	"sharedNotepad": func(l *plugnmeet.LockSettings, val *bool) { l.LockSharedNotepad = val },
}

// assignNewLockSetting use map to find and update
func (m *UserModel) assignNewLockSetting(service string, direction string, l *plugnmeet.LockSettings) {
	if setter, ok := lockSettingMap[service]; ok {
		lock := proto.Bool(false)
		if direction == "lock" {
			*lock = true
		}
		setter(l, lock)
	}
}
