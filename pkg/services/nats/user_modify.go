package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	RoomUsersBucketPrefix = Prefix + "roomUsers-"
	RoomUsersBucket       = RoomUsersBucketPrefix + "%s"

	userInfoBucketPrefix = Prefix + "userInfo-"
	UserInfoBucket       = userInfoBucketPrefix + "r_%s-u_%s"

	RoomUsersBlockList = Prefix + "usersBlockList-%s"

	UserOnlineMaxPingDiff = time.Minute * 2 // after 2 minutes we'll treat user as offline

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

func (s *NatsService) AddUser(roomId, userId, name string, isAdmin, isPresenter bool, metadata *plugnmeet.UserMetadata) error {
	// first add user to room
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf(RoomUsersBucket, roomId),
	})
	if err != nil {
		return err
	}
	// format of user, userid & value is the status
	_, err = kv.PutString(s.ctx, userId, UserStatusAdded)
	if err != nil {
		return err
	}

	// now we'll create different bucket for info
	kv, err = s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf(UserInfoBucket, roomId, userId),
	})
	if err != nil {
		return err
	}

	mt, err := s.MarshalUserMetadata(metadata)
	if err != nil {
		return err
	}

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

	for k, v := range data {
		_, err = kv.PutString(s.ctx, k, v)
		if err != nil {
			log.Errorln(err)
		}
	}

	return nil
}

func (s *NatsService) UpdateUserStatus(roomId, userId string, status string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return errors.New(fmt.Sprintf("no user found with userId: %s", userId))
	case err != nil:
		return err
	}

	_, err = kv.PutString(s.ctx, userId, status)
	if err != nil {
		return err
	}

	kv, err = s.js.KeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
	if err != nil {
		return err
	}

	// update user info for
	if status == UserStatusOnline {
		// first check if data exist
		joined, _ := kv.Get(s.ctx, UserJoinedAt)
		if joined != nil && len(joined.Value()) > 0 {
			_, err = kv.PutString(s.ctx, UserReconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
			if err != nil {
				return err
			}
		} else {
			_, err = kv.PutString(s.ctx, UserJoinedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
			if err != nil {
				return err
			}
		}
	} else if status == UserStatusDisconnected || status == UserStatusOffline {
		_, err = kv.PutString(s.ctx, UserDisconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateUserMetadata will properly update user metadata
func (s *NatsService) UpdateUserMetadata(roomId, userId string, metadata interface{}) (string, error) {
	var mt *plugnmeet.UserMetadata
	var err error

	switch v := metadata.(type) {
	case string:
		// because we'll need to update id
		mt, err = s.UnmarshalUserMetadata(v)
		if err != nil {
			return "", err
		}
	case plugnmeet.UserMetadata:
		mt = &v
	case *plugnmeet.UserMetadata:
		mt = v
	default:
		return "", errors.New("invalid metadata data type")
	}

	// id will update during marshaling
	marshal, err := s.MarshalUserMetadata(mt)
	if err != nil {
		return "", err
	}

	err = s.UpdateUserKeyValue(roomId, userId, UserMetadataKey, marshal)
	if err != nil {
		return "", err
	}

	return marshal, nil
}

func (s *NatsService) DeleteUser(roomId, userId string) {
	if kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId)); err == nil {
		_ = kv.Purge(s.ctx, userId)
	}

	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
}

func (s *NatsService) DeleteAllRoomUsersWithConsumer(roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		// nothing found
		return nil
	case err != nil:
		return err
	}

	kl, err := kv.ListKeys(s.ctx)
	if err != nil {
		return err
	}

	for u := range kl.Keys() {
		// delete bucket of the user info
		_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, u))
		// delete consumer
		s.DeleteConsumer(roomId, u)
	}

	// now delete room users bucket
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomUsersBucket, roomId))

	return nil
}

func (s *NatsService) UpdateUserKeyValue(roomId, userId, key, val string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(UserInfoBucket, roomId, userId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return errors.New(fmt.Sprintf("no user found with userId: %s", userId))
	case err != nil:
		return err
	}

	_, err = kv.PutString(s.ctx, key, val)
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) AddUserToBlockList(roomId, userId string) (uint64, error) {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf(RoomUsersBlockList, roomId),
	})
	if err != nil {
		return 0, err
	}
	return kv.PutString(s.ctx, userId, fmt.Sprintf("%d", time.Now().UnixMilli()))
}

func (s *NatsService) DeleteRoomUsersBlockList(roomId string) {
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomUsersBlockList, roomId))
}
