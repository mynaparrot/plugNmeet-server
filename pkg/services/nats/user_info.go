package natsservice

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// GetRoomUserStatus retrieves the status of a user in a specific room.
// Returns an empty string if the user or room is not found.
func (s *NatsService) GetRoomUserStatus(roomId, userId string) (string, error) {
	var status string
	if status = s.cs.GetCachedRoomUserStatus(roomId, userId); status != "" {
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
	if info := s.cs.GetUserInfo(roomId, userId); info != nil {
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

// GetRoomAllUsersFromStatusBucket will list all keys and filter for user statuses.
func (s *NatsService) GetRoomAllUsersFromStatusBucket(roomId string) (map[string]jetstream.KeyValueEntry, error) {
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
		if strings.HasPrefix(key, UserKeyPrefix) && strings.HasSuffix(key, UserKeyFieldPrefix+UserStatusKey) {
			if entry, err := kv.Get(s.ctx, key); err == nil && entry != nil {
				// parsing based on the "user_<userId>-FIELD_<field>" schema.
				trimmed := strings.TrimPrefix(key, UserKeyPrefix)
				parts := strings.SplitN(trimmed, UserKeyFieldPrefix, 2)
				if len(parts) == 2 {
					userId := parts[0]
					users[userId] = entry
				}
			}
		}
	}
	return users, nil
}

// GetOnlineUsersId retrieves the IDs of users who are currently online in a specific room.
// Returns nil if the room is not found or no users are online.
func (s *NatsService) GetOnlineUsersId(roomId string) ([]string, error) {
	if userIds := s.cs.GetUsersIdFromRoomStatusBucket(roomId, UserStatusOnline); len(userIds) > 0 {
		return userIds, nil
	}

	// fallback to nats
	users, err := s.GetRoomAllUsersFromStatusBucket(roomId)
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

func (s *NatsService) GetUsersIdFromRoomStatusBucket(roomId string) []string {
	var userIds []string
	if userIds = s.cs.GetUsersIdFromRoomStatusBucket(roomId, ""); len(userIds) > 0 {
		return userIds
	}

	// fallback to nats
	users, err := s.GetRoomAllUsersFromStatusBucket(roomId)
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
	info, err := s.GetUserInfo(roomId, userId)
	if err != nil {
		return nil, err
	}
	if info == nil || len(info.Metadata) == 0 {
		return nil, nil
	}

	return s.UnmarshalUserMetadata(info.Metadata)
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
	if val := s.cs.GetUserLastPingAt(roomId, userId); val > 0 {
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
	userInfo := s.cs.GetUserInfo(roomId, userId)
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
// Returns false if the user or room is not found.
func (s *NatsService) IsUserExistInBlockList(roomId, userId string) bool {
	kv, err := s.getKV(fmt.Sprintf(RoomUsersBlockList, roomId))
	if err != nil || kv == nil {
		return false
	}
	entry, err := kv.Get(s.ctx, userId)
	return err == nil && entry != nil
}
