package natsservice

import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// GetRoomUserStatus retrieves the status of a user in a specific room.
// Returns an empty string if the user or room is not found.
func (s *NatsService) GetRoomUserStatus(roomId, userId string) (string, error) {
	var status string
	if status = s.cs.getCachedRoomUserStatus(roomId, userId); status != "" {
		return status, nil
	}

	bucket := s.formatConsolidatedRoomBucket(roomId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return "", err
	}

	// The watcher is now on the consolidated bucket, started during room creation.
	return s.getStringValue(kv, s.formatUserKey(userId, UserStatusKey))
}

// GetUserInfo retrieves detailed information about a user in a specific room.
// Returns nil if the user or room is not found.
func (s *NatsService) GetUserInfo(roomId, userId string) (*plugnmeet.NatsKvUserInfo, error) {
	if info := s.cs.getUserInfo(roomId, userId); info != nil {
		return info, nil
	}

	bucket := s.formatConsolidatedRoomBucket(roomId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return nil, err
	}

	info := &plugnmeet.NatsKvUserInfo{}
	info.UserId, _ = s.getStringValue(kv, s.formatUserKey(userId, UserIdKey))
	info.UserSid, _ = s.getStringValue(kv, s.formatUserKey(userId, UserSidKey))
	info.Name, _ = s.getStringValue(kv, s.formatUserKey(userId, UserNameKey))
	info.RoomId, _ = s.getStringValue(kv, s.formatUserKey(userId, UserRoomIdKey))
	info.Metadata, _ = s.getStringValue(kv, s.formatUserKey(userId, UserMetadataKey))
	info.IsAdmin, _ = s.getBoolValue(kv, s.formatUserKey(userId, UserIsAdminKey))
	info.IsPresenter, _ = s.getBoolValue(kv, s.formatUserKey(userId, UserIsPresenterKey))
	info.JoinedAt, _ = s.getUint64Value(kv, s.formatUserKey(userId, UserJoinedAt))
	info.ReconnectedAt, _ = s.getUint64Value(kv, s.formatUserKey(userId, UserReconnectedAt))
	info.DisconnectedAt, _ = s.getUint64Value(kv, s.formatUserKey(userId, UserDisconnectedAt))

	// The watcher is now on the consolidated bucket, started during room creation.
	return info, nil
}

// GetRoomUserStatusEntries will list all keys and filter for user statuses.
func (s *NatsService) GetRoomUserStatusEntries(roomId string) (map[string]jetstream.KeyValueEntry, error) {
	kv, err := s.getKV(s.formatConsolidatedRoomBucket(roomId))
	if err != nil || kv == nil {
		return nil, err
	}

	kl, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	users := make(map[string]jetstream.KeyValueEntry)
	for key := range kl.Keys() {
		// Use the new helper function to parse the user key
		userId, field, ok := ParseUserKey(key)
		if ok && field == UserStatusKey {
			if entry, err := kv.Get(s.ctx, key); err == nil && entry != nil {
				users[userId] = entry
			}
		}
	}
	return users, nil
}

// GetOnlineUsersId retrieves the IDs of users who are currently online in a specific room.
// Returns nil if the room is not found or no users are online.
func (s *NatsService) GetOnlineUsersId(roomId string) ([]string, error) {
	if userIds := s.cs.getRoomUserIds(roomId, UserStatusOnline); len(userIds) > 0 {
		return userIds, nil
	}

	// fallback to nats
	users, err := s.GetRoomUserStatusEntries(roomId) // Use new name
	if err != nil || users == nil {
		return nil, err
	}

	var userIds []string
	for id, entry := range users {
		if string(entry.Value()) == UserStatusOnline {
			userIds = append(userIds, id)
		}
	}
	return userIds, nil
}

// GetRoomUserIds retrieves all user IDs for a given room.
func (s *NatsService) GetRoomUserIds(roomId string) []string {
	var userIds []string
	if userIds = s.cs.getRoomUserIds(roomId, ""); len(userIds) > 0 {
		return userIds
	}

	// fallback to nats
	users, err := s.GetRoomUserStatusEntries(roomId)
	if err != nil || users == nil {
		return userIds
	}

	for id := range users {
		userIds = append(userIds, id)
	}
	return userIds
}

// GetOnlineUsersList retrieves detailed information about all online users in a specific room.
// Returns nil if the room is not found or no users are online.
func (s *NatsService) GetOnlineUsersList(roomId string) ([]*plugnmeet.NatsKvUserInfo, error) {
	userIds, err := s.GetOnlineUsersId(roomId)
	if err != nil || len(userIds) == 0 {
		return nil, err
	}

	var users []*plugnmeet.NatsKvUserInfo
	for _, id := range userIds {
		info, err := s.GetUserInfo(roomId, id)
		if err != nil {
			return nil, err
		}
		if info != nil {
			users = append(users, info)
		}
	}
	return users, nil
}

// GetOnlineUsersListAsJson retrieves detailed information about all online users in a specific room as JSON.
// Returns nil if the room is not found or no users are online.
func (s *NatsService) GetOnlineUsersListAsJson(roomId string) ([]byte, error) {
	users, err := s.GetOnlineUsersList(roomId)
	if err != nil || len(users) == 0 {
		return nil, err
	}

	raw := make([]json.RawMessage, len(users))
	for i, u := range users {
		r, err := protoJsonOpts.Marshal(u)
		if err != nil {
			return nil, err
		}
		raw[i] = r
	}
	return json.Marshal(raw)
}

// GetUserKeyValue retrieves a specific key-value entry for a user in a specific room.
// Returns nil if the user or room is not found.
func (s *NatsService) GetUserKeyValue(roomId, userId, key string) (jetstream.KeyValueEntry, error) {
	kv, err := s.getKV(s.formatConsolidatedRoomBucket(roomId))
	if err != nil || kv == nil {
		return nil, err
	}
	return kv.Get(s.ctx, s.formatUserKey(userId, key))
}

// GetUserMetadataStruct retrieves the metadata for a user in a specific room as a structured object.
// Returns nil if the user or room is not found.
func (s *NatsService) GetUserMetadataStruct(roomId, userId string) (*plugnmeet.UserMetadata, error) {
	// Use the dedicated cache method to get only the metadata.
	if metadata, found := s.cs.getCachedUserMetadata(roomId, userId); found {
		if len(metadata) > 0 {
			return s.UnmarshalUserMetadata(metadata)
		}
		return nil, nil // Metadata is empty in cache
	}

	// If not in cache, directly fetch only the metadata key from NATS KV.
	kv, err := s.getKV(s.formatConsolidatedRoomBucket(roomId))
	if err != nil || kv == nil {
		return nil, err
	}

	entry, err := kv.Get(s.ctx, s.formatUserKey(userId, UserMetadataKey))
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, nil // Metadata key not found
		}
		return nil, err
	}

	if entry == nil || len(entry.Value()) == 0 {
		return nil, nil
	}

	return s.UnmarshalUserMetadata(string(entry.Value()))
}

// GetUserWithMetadata retrieves detailed information and metadata about a user in a specific room.
// Returns nil if the user or room is not found.
func (s *NatsService) GetUserWithMetadata(roomId, userId string) (*plugnmeet.NatsKvUserInfo, *plugnmeet.UserMetadata, error) {
	info, err := s.GetUserInfo(roomId, userId)
	if err != nil || info == nil {
		return nil, nil, err
	}
	metadata, err := s.UnmarshalUserMetadata(info.Metadata)
	if err != nil {
		return nil, nil, err
	}
	return info, metadata, nil
}

// GetUserLastPing retrieves the last ping timestamp for a user in a specific room.
// Returns 0 if the user or room is not found or the timestamp cannot be parsed.
func (s *NatsService) GetUserLastPing(roomId, userId string) int64 {
	if val := s.cs.getUserLastPingAt(roomId, userId); val > 0 {
		return val
	}

	val, err := s.GetUserKeyValue(roomId, userId, UserLastPingAt)
	if err != nil || val == nil {
		return 0
	}
	if ts, err := strconv.ParseInt(string(val.Value()), 10, 64); err == nil {
		return ts
	}
	return 0
}

// IsUserPresenter checks if a user is a presenter in a specific room.
// Returns false if the user or room is not found.
func (s *NatsService) IsUserPresenter(roomId, userId string) bool {
	// check cache first
	userInfo := s.cs.getUserInfo(roomId, userId)
	if userInfo != nil {
		return userInfo.GetIsPresenter()
	}

	// fallback to nats
	val, err := s.GetUserKeyValue(roomId, userId, UserIsPresenterKey)
	if err != nil || val == nil {
		return false
	}
	return string(val.Value()) == "true"
}

// IsUserExistInBlockList checks if a user is in the block list for a specific room.
// It checks the cache first for performance.
func (s *NatsService) IsUserExistInBlockList(roomId, userId string) bool {
	// Check cache first
	if isBlocked, found := s.cs.isUserBlacklistedFromCache(roomId, userId); found {
		return isBlocked
	}

	// Fallback to NATS if not in cache
	kv, err := s.getKV(s.formatConsolidatedRoomBucket(roomId))
	if err != nil || kv == nil {
		return false
	}

	// Check for the existence and value of the is_blacklisted key.
	entry, err := kv.Get(s.ctx, s.formatUserKey(userId, UserIsBlacklistedKey))
	if err != nil || entry == nil {
		return false
	}

	isBlocked, _ := strconv.ParseBool(string(entry.Value()))
	return isBlocked
}
