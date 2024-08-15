package natsservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	RoomUsers      = Prefix + "roomUsers"
	UserInfoBucket = Prefix + "userInfo"

	userIdKey       = "id"
	userSidKey      = "sid"
	userNameKey     = "name"
	userRoomIdKey   = "room_id"
	userMetadataKey = "metadata"

	UserAdded        = "added"
	UserOnline       = "online"
	UserDisconnected = "disconnected"
	UserOffline      = "offline"
)

func (s *NatsService) AddUser(roomId, userId, sid, name string, metadata *plugnmeet.UserMetadata) error {
	// first add user to room
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf("%s-%s", RoomUsers, roomId),
	})
	if err != nil {
		return err
	}
	// format of user, userid & value is the status
	_, err = kv.Put(s.ctx, userId, []byte(UserAdded))
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

	_, err = kv.Put(s.ctx, userIdKey, []byte(userId))
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, userSidKey, []byte(sid))
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, userNameKey, []byte(name))
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, userRoomIdKey, []byte(roomId))
	if err != nil {
		return err
	}

	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, userMetadataKey, []byte(mt))
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) GetRoomUserStatus(roomId, userId string) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return "", nil
	case err != nil:
		return "", err
	}

	status, err := kv.Get(s.ctx, userId)
	if err != nil {
		return "", err
	}

	return string(status.Value()), nil
}

func (s *NatsService) UpdateUserStatus(roomId, userId string, status string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	_, err = kv.Put(s.ctx, userId, []byte(status))
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) GetUserInfo(userId string) (*plugnmeet.NatsKvUserInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	id, _ := kv.Get(s.ctx, userIdKey)
	sid, _ := kv.Get(s.ctx, userSidKey)
	name, _ := kv.Get(s.ctx, userNameKey)
	roomId, _ := kv.Get(s.ctx, userRoomIdKey)
	metadata, _ := kv.Get(s.ctx, userMetadataKey)

	info := &plugnmeet.NatsKvUserInfo{
		UserId:   string(id.Value()),
		UserSid:  string(sid.Value()),
		Name:     string(name.Value()),
		RoomId:   string(roomId.Value()),
		Metadata: string(metadata.Value()),
	}

	return info, nil
}

// UpdateUserInfo will basically update metadata only
// because normally we do not need to update other info
func (s *NatsService) UpdateUserInfo(userId string, metadata *plugnmeet.UserMetadata) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	if err != nil {
		return "", err
	}

	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return "", err
	}

	_, err = kv.Put(s.ctx, userMetadataKey, []byte(mt))
	if err != nil {
		return "", err
	}

	return mt, nil
}

func (s *NatsService) DeleteUser(roomId, userId string) {
	if kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId)); err == nil {
		_ = kv.Delete(s.ctx, userId)
	}

	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
}

func (s *NatsService) DeleteAllRoomUsers(roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		// nothing found
		return nil
	case err != nil:
		return err
	}

	users, err := kv.Keys(s.ctx)
	if err != nil {
		return err
	}

	for _, u := range users {
		// delete bucket of the user info
		_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, u))
	}

	// now delete room users bucket
	_ = s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId))

	return nil
}
