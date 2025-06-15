package natsservice

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// Constants for bucket naming and user metadata keys
const (
	RoomUsersBucketPrefix = Prefix + "roomUsers-"
	RoomUsersBucket       = RoomUsersBucketPrefix + "%s"

	userInfoBucketPrefix = Prefix + "userInfo-"
	UserInfoBucket       = userInfoBucketPrefix + "r_%s-u_%s"

	RoomUsersBlockList = Prefix + "usersBlockList-%s"

	UserOnlineMaxPingDiff = time.Minute * 2

	UserIdKey          = "id"
	UserSidKey         = "sid"
	UserNameKey        = "name"
	UserRoomIdKey      = "room_id"
	UserIsAdminKey     = "is_admin"
	UserIsPresenterKey = "is_presenter"
	UserMetadataKey    = "metadata"
	UserJoinedAt       = "joined_at"
	UserReconnectedAt  = "reconnected_at"
	UserDisconnectedAt = "disconnected_at"
	UserLastPingAt     = "last_ping_at"

	UserStatusAdded        = "added"
	UserStatusOnline       = "online"
	UserStatusDisconnected = "disconnected"
	UserStatusOffline      = "offline"
)

// AddUser adds a new user to a room and stores their metadata
func (s *NatsService) AddUser(roomId, userId, name string, isAdmin, isPresenter bool, metadata *plugnmeet.UserMetadata) error {
	// Create or update the room users bucket
	bucket := fmt.Sprintf(RoomUsersBucket, roomId)
	roomKV, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   bucket,
	})
	if err != nil {
		return fmt.Errorf("failed to create room bucket: %w", err)
	}

	// Add user status to the room bucket
	if _, err := roomKV.PutString(s.ctx, userId, UserStatusAdded); err != nil {
		return fmt.Errorf("failed to add user to room: %w", err)
	}
	// add watcher for user status bucket
	s.cs.AddRoomUserStatusWatcher(roomKV, bucket, roomId)

	// Create or update the user info bucket
	bucket = fmt.Sprintf(UserInfoBucket, roomId, userId)
	userKV, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   bucket,
	})
	if err != nil {
		return fmt.Errorf("failed to create user info bucket: %w", err)
	}

	// Marshal metadata
	mt, err := s.MarshalUserMetadata(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Prepare user data
	data := map[string]string{
		UserIdKey:          userId,
		UserSidKey:         uuid.NewString(),
		UserNameKey:        name,
		UserRoomIdKey:      roomId,
		UserIsAdminKey:     fmt.Sprintf("%v", isAdmin),
		UserIsPresenterKey: fmt.Sprintf("%v", isPresenter),
		UserMetadataKey:    mt,
		UserLastPingAt:     "0",
	}

	// Store user data in the key-value store
	for k, v := range data {
		if _, err := userKV.PutString(s.ctx, k, v); err != nil {
			return fmt.Errorf("failed to add user to user bucket: %w", err)
		}
	}

	// add to user info watcher
	s.cs.AddUserInfoWatcher(userKV, bucket, roomId, userId)

	return nil
}

// UpdateUserStatus updates the status of a user in a room
func (s *NatsService) UpdateUserStatus(roomId, userId string, status string) error {
	// Retrieve the room users bucket
	roomKV, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("no user found with userId: %s", userId)
		}
		return err
	}

	// Update user status in the room bucket
	if _, err := roomKV.PutString(s.ctx, userId, status); err != nil {
		return fmt.Errorf("failed to update user status: %w", err)
	}

	// Retrieve the user info bucket
	userKV, err := s.js.KeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
	if err != nil {
		return fmt.Errorf("failed to retrieve user info bucket: %w", err)
	}

	// Update user info based on status
	switch status {
	case UserStatusOnline:
		// Check if user has joined before
		joined, _ := userKV.Get(s.ctx, UserJoinedAt)
		if joined != nil && len(joined.Value()) > 0 {
			if _, err := userKV.PutString(s.ctx, UserReconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli())); err != nil {
				return fmt.Errorf("failed to update reconnected time: %w", err)
			}
		} else {
			if _, err := userKV.PutString(s.ctx, UserJoinedAt, fmt.Sprintf("%d", time.Now().UnixMilli())); err != nil {
				return fmt.Errorf("failed to update joined time: %w", err)
			}
		}
	case UserStatusDisconnected, UserStatusOffline:
		if _, err := userKV.PutString(s.ctx, UserDisconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli())); err != nil {
			return fmt.Errorf("failed to update disconnected time: %w", err)
		}
	}

	return nil
}

// UpdateUserMetadata updates the metadata of a user
func (s *NatsService) UpdateUserMetadata(roomId, userId string, metadata interface{}) (string, error) {
	var mt *plugnmeet.UserMetadata
	var err error

	// Determine the type of metadata and unmarshal accordingly
	switch v := metadata.(type) {
	case string:
		mt, err = s.UnmarshalUserMetadata(v)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	case plugnmeet.UserMetadata:
		mt = &v
	case *plugnmeet.UserMetadata:
		mt = v
	default:
		return "", errors.New("invalid metadata data type")
	}

	// Marshal the updated metadata
	marshal, err := s.MarshalUserMetadata(mt)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Update the user metadata in the key-value store
	if err := s.UpdateUserKeyValue(roomId, userId, UserMetadataKey, marshal); err != nil {
		return "", fmt.Errorf("failed to update user metadata: %w", err)
	}

	return marshal, nil
}

// DeleteUser removes a user from a room and deletes their metadata
func (s *NatsService) DeleteUser(roomId, userId string) {
	// Retrieve and purge the user from the room users bucket
	if roomKV, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId)); err == nil {
		_ = roomKV.Purge(s.ctx, userId)
	}

	// Delete the user info bucket
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
}

// DeleteAllRoomUsersWithConsumer deletes all users from a room and their associated consumers
func (s *NatsService) DeleteAllRoomUsersWithConsumer(roomId string) error {
	// Retrieve the room users bucket
	roomKV, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil
		}
		return err
	}

	// List all user keys in the room users bucket
	kl, err := roomKV.ListKeys(s.ctx)
	if err != nil {
		return fmt.Errorf("failed to list user keys: %w", err)
	}

	// Delete each user's info bucket and associated consumer
	for u := range kl.Keys() {
		_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, u))
		s.DeleteConsumer(roomId, u)
	}

	// Delete the room users bucket
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))

	return nil
}

// UpdateUserKeyValue updates a specific key-value pair for a user
func (s *NatsService) UpdateUserKeyValue(roomId, userId, key, val string) error {
	// Retrieve the user info bucket
	userKV, err := s.js.KeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("no user found with userId: %s", userId)
		}
		return err
	}

	// Update the key-value pair in the user info bucket
	if _, err := userKV.PutString(s.ctx, key, val); err != nil {
		return fmt.Errorf("failed to update key-value pair: %w", err)
	}

	return nil
}

// AddUserToBlockList adds a user to the block list for a room
func (s *NatsService) AddUserToBlockList(roomId, userId string) (uint64, error) {
	// Create or update the room users block list bucket
	blockListKV, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket:   fmt.Sprintf(RoomUsersBlockList, roomId),
		Replicas: s.app.NatsInfo.NumReplicas,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create block list bucket: %w", err)
	}

	// Add the user to the block list with the current timestamp
	return blockListKV.PutString(s.ctx, userId, fmt.Sprintf("%d", time.Now().UnixMilli()))
}

// DeleteRoomUsersBlockList deletes the block list for a room
func (s *NatsService) DeleteRoomUsersBlockList(roomId string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomUsersBlockList, roomId))
}
