package natsservice

import (
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
)

// setUserInfoCache is the single entry point for updating the user info cache.
// It can be called either by the NATS watcher or manually.
func (ncs *NatsCacheService) setUserInfoCache(roomId, userId, field, value string, revision uint64) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()

	room, roomOk := ncs.roomUsersInfoStore[roomId]
	if !roomOk {
		// This can happen if the room was cleaned up just after the event was dispatched.
		// We should not create it manually as UserInfo map belongs to a room
		// and should only be created when we'll create room map e.g. in addRoomWatcher
		// It's safe to just return.
		return
	}

	user, userOk := room[userId]
	if !userOk {
		user = CachedUserInfoEntry{UserInfo: new(plugnmeet.NatsKvUserInfo)}
	}

	// If the incoming revision is older than what we have, ignore it.
	if revision > 0 && user.LastRevision >= revision {
		return
	}

	switch field {
	case UserIdKey:
		user.UserInfo.UserId = value
	case UserSidKey:
		user.UserInfo.UserSid = value
	case UserNameKey:
		user.UserInfo.Name = value
	case UserRoomIdKey:
		user.UserInfo.RoomId = value
	case UserMetadataKey:
		user.UserInfo.Metadata = value
	case UserIsAdminKey:
		user.UserInfo.IsAdmin, _ = strconv.ParseBool(value)
	case UserIsPresenterKey:
		user.UserInfo.IsPresenter, _ = strconv.ParseBool(value)
	case UserIsBlacklistedKey:
		user.IsBlacklisted, _ = strconv.ParseBool(value)
	case UserJoinedAt:
		user.UserInfo.JoinedAt = ncs.convertTextToUint64(value)
	case UserReconnectedAt:
		user.UserInfo.ReconnectedAt = ncs.convertTextToUint64(value)
	case UserDisconnectedAt:
		user.UserInfo.DisconnectedAt = ncs.convertTextToUint64(value)
	case UserLastPingAt:
		user.LastPingAt = ncs.convertTextToUint64(value)
	case UserStatusKey:
		user.Status = value
	case UserTurnCredentialsKey:
		user.TurnCredentials = value
	}

	user.LastRevision = revision
	ncs.roomUsersInfoStore[roomId][userId] = user
}

// getCachedRoomUserStatus reads the user status from the unified cache.
func (ncs *NatsCacheService) getCachedRoomUserStatus(roomId, userId string) string {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok {
			return entry.Status
		}
	}
	return ""
}

// getRoomUserIds reads user IDs from the unified cache, filtering by status.
func (ncs *NatsCacheService) getRoomUserIds(roomId, filterStatus string) []string {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()

	var usersIds []string
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		for userId, val := range rm {
			if filterStatus != "" && val.Status == filterStatus {
				usersIds = append(usersIds, userId)
			} else if filterStatus == "" {
				usersIds = append(usersIds, userId)
			}
		}
	}
	return usersIds
}

// getUserInfo is a simple reader for the cache.
func (ncs *NatsCacheService) getUserInfo(roomId, userId string) *plugnmeet.NatsKvUserInfo {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok && entry.UserInfo != nil && entry.UserInfo.UserId != "" {
			infoCopy := proto.Clone(entry.UserInfo).(*plugnmeet.NatsKvUserInfo)
			return infoCopy
		}
	}
	return nil
}

// getCachedUserMetadata retrieves only the user's metadata string from the cache.
// It returns the metadata and a boolean indicating if it was found.
func (ncs *NatsCacheService) getCachedUserMetadata(roomId, userId string) (string, bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok && entry.UserInfo != nil && entry.UserInfo.Metadata != "" {
			return entry.UserInfo.Metadata, true
		}
	}
	return "", false
}

// isUserBlacklistedFromCache is a simple reader for the cache.
// It returns the status, and a boolean indicating if the value was found in the cache.
func (ncs *NatsCacheService) isUserBlacklistedFromCache(roomId, userId string) (isBlocked bool, foundInCache bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok {
			return entry.IsBlacklisted, true
		}
	}
	return false, false
}

// getCachedUserTurnCredentials retrieves only the user's turn credentials string from the cache.
func (ncs *NatsCacheService) getCachedUserTurnCredentials(roomId, userId string) (string, bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok && entry.TurnCredentials != "" {
			return entry.TurnCredentials, true
		}
	}
	return "", false
}

// getUserLastPingAt is a simple reader for the cache.
func (ncs *NatsCacheService) getUserLastPingAt(roomId, userId string) int64 {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok {
			return int64(entry.LastPingAt)
		}
	}
	return 0
}
