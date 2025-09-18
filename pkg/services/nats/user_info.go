package natsservice

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// GetRoomUserStatus retrieves the status of a user in a specific room.
// Returns an empty string if the user or room is not found.
func (s *NatsService) GetRoomUserStatus(roomId, userId string) (string, error) {
	var status string
	if status, _ = s.cs.GetCachedRoomUserStatus(roomId, userId); status != "" {
		return status, nil
	}

	bucket := fmt.Sprintf(RoomUsersBucket, roomId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return "", err
	}

	// So, for some reason, not found in cache, then may be user wasn't added to this room. So, we will start watching
	s.cs.AddRoomUserStatusWatcher(kv, bucket, roomId)

	return s.getStringValue(kv, userId)
}

// GetUserInfo retrieves detailed information about a user in a specific room.
// Returns nil if the user or room is not found.
func (s *NatsService) GetUserInfo(roomId, userId string) (*plugnmeet.NatsKvUserInfo, error) {
	if info := s.cs.GetUserInfo(roomId, userId); info != nil {
		return info, nil
	}

	bucket := fmt.Sprintf(UserInfoBucket, roomId, userId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return nil, err
	}

	info := &plugnmeet.NatsKvUserInfo{}
	info.UserId, _ = s.getStringValue(kv, UserIdKey)
	info.UserSid, _ = s.getStringValue(kv, UserSidKey)
	info.Name, _ = s.getStringValue(kv, UserNameKey)
	info.RoomId, _ = s.getStringValue(kv, UserRoomIdKey)
	info.Metadata, _ = s.getStringValue(kv, UserMetadataKey)
	info.IsAdmin, _ = s.getBoolValue(kv, UserIsAdminKey)
	info.IsPresenter, _ = s.getBoolValue(kv, UserIsPresenterKey)
	info.JoinedAt, _ = s.getUint64Value(kv, UserJoinedAt)
	info.ReconnectedAt, _ = s.getUint64Value(kv, UserReconnectedAt)
	info.DisconnectedAt, _ = s.getUint64Value(kv, UserDisconnectedAt)

	// So, for some reason, not found in cache. So, we will start watching
	s.cs.AddUserInfoWatcher(kv, bucket, roomId, userId)

	return info, nil
}

// GetRoomAllUsersFromStatusBucket retrieves all users and their statuses in a specific room.
// Returns nil if the room is not found.
func (s *NatsService) GetRoomAllUsersFromStatusBucket(roomId string) (map[string]jetstream.KeyValueEntry, error) {
	kv, err := s.getKV(fmt.Sprintf(RoomUsersBucket, roomId))
	if err != nil || kv == nil {
		return nil, err
	}

	kl, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	users := make(map[string]jetstream.KeyValueEntry)
	for k := range kl.Keys() {
		if entry, err := kv.Get(s.ctx, k); err == nil && entry != nil {
			users[k] = entry
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
	kv, err := s.getKV(fmt.Sprintf(UserInfoBucket, roomId, userId))
	if err != nil || kv == nil {
		return nil, err
	}
	return kv.Get(s.ctx, key)
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
