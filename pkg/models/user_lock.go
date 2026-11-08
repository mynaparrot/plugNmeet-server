package models

import (
	"reflect"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
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
func (m *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) (plugnmeet.StatusCode, error) {
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
	}

	// For a single user, perform the update and broadcast immediately.
	log.Info("request to update single user lock settings")
	return m.updateAndBroadcastUserLock(log, r.RoomId, r.UserId, r.Service, r.Direction, false)
}

// handleUpdateAllUsersLockSettings orchestrates the update for all users in a room.
func (m *UserModel) handleUpdateAllUsersLockSettings(r *plugnmeet.UpdateUserLockSettingsReq, log *logrus.Entry) (plugnmeet.StatusCode, error) {
	log.Info("request to update all users lock settings")

	// First, update the room's default settings for any future users.
	if statusCode, err := m.updateDefaultRoomLockSettings(r); err != nil {
		// Log the error but continue, as updating existing users is also important.
		log.WithError(err).Errorln("failed to update default room lock settings")
		if statusCode == plugnmeet.StatusCode_ROOM_NOT_FOUND {
			// the room is gone, updating its online users can't succeed either
			return statusCode, err
		}
	}

	// Get the list of all online users to update them.
	userIds, err := m.natsService.GetOnlineUsersId(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get online users list")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}

	for _, id := range userIds {
		if id == r.RequestedUserId {
			// nothing for requested user as is admin
			continue
		}
		if _, err := m.updateAndBroadcastUserLock(log, r.RoomId, id, r.Service, r.Direction, true); err != nil {
			log.WithError(err).WithField("user_id", id).Errorln("failed to update user lock settings during all-user change")
		}
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}

// updateAndBroadcastUserLock is the single, reusable worker function that updates
// a specific user's lock settings and broadcasts the change.
func (m *UserModel) updateAndBroadcastUserLock(log *logrus.Entry, roomId, userId, service, direction string, forAllUserTask bool) (plugnmeet.StatusCode, error) {
	log = log.WithFields(logrus.Fields{
		"target_user_id": userId,
		"service":        service,
		"direction":      direction,
	})

	mt, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil {
		log.WithError(err).Errorln("failed to get user metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}
	if mt == nil {
		log.Warnln("user metadata not found")
		return plugnmeet.StatusCode_USER_NOT_FOUND, config.ErrUserNotFound
	}

	// when target user is admin, we should not allow lock/unlock except for whiteboard
	if mt.IsAdmin && service != "whiteboard" {
		if forAllUserTask {
			// silently skip when setting global locking for all users
			return plugnmeet.StatusCode_SUCCESS, nil
		}
		// return error for individual user updates
		log.Warnln("cannot change lock settings of another admin")
		return plugnmeet.StatusCode_PERMISSION_DENIED, config.ErrUserCannotLockAdmin
	}

	// Apply the new setting to the struct.
	m.assignNewLockSetting(service, direction, mt.LockSettings)

	// Persist the change and notify the clients.
	if err := m.natsService.UpdateAndBroadcastUserMetadata(roomId, userId, mt, nil); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast user metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}

	return plugnmeet.StatusCode_SUCCESS, nil
}

// updateDefaultRoomLockSettings updates the room's default lock settings for future users.
func (m *UserModel) updateDefaultRoomLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) (plugnmeet.StatusCode, error) {
	mt, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		m.logger.WithError(err).Errorln("failed to get room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}
	if mt == nil {
		m.logger.WithField("room_id", r.RoomId).Warnln("room metadata not found")
		return plugnmeet.StatusCode_ROOM_NOT_FOUND, config.ErrRoomNotFound
	}

	m.assignNewLockSetting(r.Service, r.Direction, mt.DefaultLockSettings)
	// Update the room metadata and broadcast the change.
	if err := m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, mt); err != nil {
		m.logger.WithError(err).Errorln("failed to update and broadcast room metadata")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrUserUnexpectedError
	}

	return plugnmeet.StatusCode_SUCCESS, nil
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
	"reactions":     func(l *plugnmeet.LockSettings, val *bool) { l.LockReactions = val },
}

// assignNewLockSetting use map to find and update
func (m *UserModel) assignNewLockSetting(service string, direction string, l *plugnmeet.LockSettings) {
	if setter, ok := lockSettingMap[service]; ok {
		lock := new(false)
		if direction == "lock" {
			*lock = true
		}
		setter(l, lock)
	}
}
