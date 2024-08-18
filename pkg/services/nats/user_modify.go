package natsservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	RoomUsersBucket       = Prefix + "roomUsers"
	UserInfoBucket        = Prefix + "userInfo"
	UserOnlineMaxPingDiff = time.Second * 30 // after 30 seconds we'll treat user as offline

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

	UserAdded        = "added"
	UserOnline       = "online"
	UserDisconnected = "disconnected"
	UserOffline      = "offline"
)

func (s *NatsService) AddUser(roomId, userId, sid, name string, isAdmin, isPresenter bool, metadata *plugnmeet.UserMetadata) error {
	// first add user to room
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf("%s-%s", RoomUsersBucket, roomId),
	})
	if err != nil {
		return err
	}
	// format of user, userid & value is the status
	_, err = kv.PutString(s.ctx, userId, UserAdded)
	if err != nil {
		return err
	}

	// now we'll create different bucket for info
	kv, err = s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf("%s-%s", UserInfoBucket, userId),
	})
	if err != nil {
		return err
	}

	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return err
	}

	data := map[string]string{
		UserIdKey:          userId,
		UserSidKey:         sid,
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
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsersBucket, roomId))
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

	kv, err = s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	if err != nil {
		return err
	}

	// update user info for
	if status == UserOnline {
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
	} else if status == UserDisconnected || status == UserOffline {
		_, err = kv.PutString(s.ctx, UserDisconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateUserMetadata will basically update metadata only
// because normally we do not need to update other info
func (s *NatsService) UpdateUserMetadata(userId string, metadata *plugnmeet.UserMetadata) (string, error) {
	// is will update during marshaling
	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return "", err
	}

	err = s.UpdateUserKeyValue(userId, UserMetadataKey, mt)
	if err != nil {
		return "", err
	}

	return mt, nil
}

func (s *NatsService) DeleteUser(roomId, userId string) {
	if kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsersBucket, roomId)); err == nil {
		_ = kv.Delete(s.ctx, userId)
	}

	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
}

func (s *NatsService) DeleteAllRoomUsers(roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsersBucket, roomId))
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
		_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, u))
	}

	// now delete room users bucket
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsersBucket, roomId))

	return nil
}

func (s *NatsService) UpdateUserKeyValue(userId, key, val string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
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
