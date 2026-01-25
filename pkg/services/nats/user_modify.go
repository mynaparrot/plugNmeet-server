package natsservice

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// Constants for bucket naming and user metadata keys
const (
	UserOnlineMaxPingDiff = time.Minute * 2

	UserIdKey            = "id"
	UserSidKey           = "sid"
	UserNameKey          = "name"
	UserRoomIdKey        = "room_id"
	UserIsAdminKey       = "is_admin"
	UserIsPresenterKey   = "is_presenter"
	UserMetadataKey      = "metadata"
	UserJoinedAt         = "joined_at"
	UserReconnectedAt    = "reconnected_at"
	UserDisconnectedAt   = "disconnected_at"
	UserLastPingAt       = "last_ping_at"
	UserStatusKey        = "status" // Note: This is different from RoomStatusKey
	UserIsBlacklistedKey = "is_blacklisted"

	UserStatusAdded        = "added"
	UserStatusOnline       = "online"
	UserStatusDisconnected = "disconnected"
	UserStatusOffline      = "offline"
)

// AddUser adds a new user to a room and stores their metadata in the consolidated bucket
func (s *NatsService) AddUser(roomId, userId, name string, isAdmin, isPresenter bool, metadata *plugnmeet.UserMetadata) error {
	// Get the consolidated room bucket
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		// If the bucket doesn't exist, it means the room wasn't created properly.
		return fmt.Errorf("failed to get consolidated room bucket: %w", err)
	}

	// Marshal metadata
	mt, err := s.MarshalUserMetadata(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Prepare user data
	data := map[string]string{
		UserIdKey:            userId,
		UserSidKey:           uuid.NewString(),
		UserNameKey:          name,
		UserRoomIdKey:        roomId,
		UserIsAdminKey:       fmt.Sprintf("%v", isAdmin),
		UserIsPresenterKey:   fmt.Sprintf("%v", isPresenter),
		UserMetadataKey:      mt,
		UserLastPingAt:       "0",
		UserStatusKey:        UserStatusAdded,
		UserIsBlacklistedKey: "false", // Set default value on creation
	}

	// Store user data in the key-value store using the user-specific prefix
	for k, v := range data {
		if _, err := kv.PutString(s.ctx, s.formatUserKey(userId, k), v); err != nil {
			return fmt.Errorf("failed to add user data to consolidated bucket for key %s: %w", k, err)
		}
	}

	// The watcher for user info is the same as the room watcher now.
	return nil
}

// UpdateUserStatus updates the status of a user in a room
func (s *NatsService) UpdateUserStatus(roomId, userId string, status string) error {
	// Retrieve the consolidated room bucket
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("no consolidated room bucket found with roomId: %s", roomId)
		}
		return err
	}

	// Update user status in the room bucket
	if _, err := kv.PutString(s.ctx, s.formatUserKey(userId, UserStatusKey), status); err != nil {
		return fmt.Errorf("failed to update user status: %w", err)
	}

	// Update user info based on status
	now := fmt.Sprintf("%d", time.Now().UnixMilli())
	switch status {
	case UserStatusOnline:
		// Check if user has joined before
		joined, _ := kv.Get(s.ctx, s.formatUserKey(userId, UserJoinedAt))
		if joined != nil && len(joined.Value()) > 0 {
			if _, err := kv.PutString(s.ctx, s.formatUserKey(userId, UserReconnectedAt), now); err != nil {
				return fmt.Errorf("failed to update reconnected time: %w", err)
			}
		} else {
			if _, err := kv.PutString(s.ctx, s.formatUserKey(userId, UserJoinedAt), now); err != nil {
				return fmt.Errorf("failed to update joined time: %w", err)
			}
		}
	case UserStatusDisconnected, UserStatusOffline:
		if _, err := kv.PutString(s.ctx, s.formatUserKey(userId, UserDisconnectedAt), now); err != nil {
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

// DeleteUser marks a user as offline in the consolidated bucket.
// Physical data deletion happens when the room bucket is deleted.
func (s *NatsService) DeleteUser(roomId, userId string) {
	_ = s.UpdateUserStatus(roomId, userId, UserStatusOffline)
}

// deleteAllUserConsumers deletes all NATS consumers associated with users in a room.
// This function is intended for internal use during room cleanup.
func (s *NatsService) deleteAllUserConsumers(roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil // Room bucket already gone, nothing to do.
		}
		return err
	}

	// List all keys to find user-specific keys and extract user IDs.
	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		return fmt.Errorf("failed to list keys for consumer deletion: %w", err)
	}

	deletedUsers := make(map[string]bool)
	for key := range keys.Keys() {
		if strings.HasPrefix(key, UserKeyPrefix) {
			// parsing based on the "user_<userId>-FIELD_<field>" schema.
			trimmed := strings.TrimPrefix(key, UserKeyPrefix)
			parts := strings.SplitN(trimmed, UserKeyFieldPrefix, 2)

			if len(parts) == 2 {
				userId := parts[0]
				if !deletedUsers[userId] {
					s.DeleteConsumer(roomId, userId)
					deletedUsers[userId] = true
				}
			}
		}
	}

	return nil
}

// UpdateUserKeyValue updates a specific key-value pair for a user
func (s *NatsService) UpdateUserKeyValue(roomId, userId, key, val string) error {
	// Retrieve the user info bucket
	userKV, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("no consolidated room bucket found with roomId: %s", roomId)
		}
		return err
	}

	// Update the key-value pair in the user info bucket
	if _, err := userKV.PutString(s.ctx, s.formatUserKey(userId, key), val); err != nil {
		return fmt.Errorf("failed to update key-value pair: %w", err)
	}

	return nil
}

// AddUserToBlockList sets the is_blacklisted flag for a user to true.
func (s *NatsService) AddUserToBlockList(roomId, userId string) error {
	// Get the consolidated room bucket
	kv, err := s.js.KeyValue(s.ctx, s.formatConsolidatedRoomBucket(roomId))
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return fmt.Errorf("no consolidated room bucket found with roomId: %s", roomId)
		}
		return err
	}

	// Directly set the blacklisted key to true. This will create the key if it doesn't exist.
	_, err = kv.PutString(s.ctx, s.formatUserKey(userId, UserIsBlacklistedKey), "true")
	if err != nil {
		return err
	}
	return nil
}

func (s *NatsService) AddUserManuallyAndBroadcast(roomId, userId, name string, isAdmin, broadcast bool) (*plugnmeet.NatsKvUserInfo, error) {
	mt := plugnmeet.UserMetadata{
		IsAdmin:         isAdmin,
		RecordWebcam:    proto.Bool(false),
		WaitForApproval: false,
		LockSettings: &plugnmeet.LockSettings{
			LockWebcam:     proto.Bool(false),
			LockMicrophone: proto.Bool(false),
		},
	}
	err := s.AddUser(roomId, userId, name, isAdmin, false, &mt)
	if err != nil {
		log.WithError(err).Errorln("failed to add ingress user to NATS")
		return nil, err
	}
	if !broadcast {
		return nil, nil
	}

	// Do proper user status update
	err = s.UpdateUserStatus(roomId, userId, UserStatusOnline)
	if err != nil {
		return nil, err
	}

	userInfo, err := s.GetUserInfo(roomId, userId)
	if err != nil {
		return nil, err
	}

	err = s.BroadcastSystemEventToEveryoneExceptUserId(plugnmeet.NatsMsgServerToClientEvents_USER_JOINED, roomId, userInfo, userId)
	if err != nil {
		return nil, err
	}
	return userInfo, nil
}
