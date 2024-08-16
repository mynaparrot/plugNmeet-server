package natsservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"strconv"
	"time"
)

const (
	RoomUsers      = Prefix + "roomUsers"
	UserInfoBucket = Prefix + "userInfo"

	userIdKey          = "id"
	userSidKey         = "sid"
	userNameKey        = "name"
	userRoomIdKey      = "room_id"
	userMetadataKey    = "metadata"
	userJoinedAt       = "joined_at"
	userReconnectedAt  = "reconnected_at"
	userDisconnectedAt = "disconnected_at"

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

	_, err = kv.PutString(s.ctx, userIdKey, userId)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, userSidKey, sid)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, userNameKey, name)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, userRoomIdKey, roomId)
	if err != nil {
		return err
	}

	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, userMetadataKey, mt)
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
		joined, _ := kv.Get(s.ctx, userJoinedAt)
		if joined != nil && len(joined.Value()) > 0 {
			_, err = kv.PutString(s.ctx, userReconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
			if err != nil {
				return err
			}
		} else {
			_, err = kv.PutString(s.ctx, userJoinedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
			if err != nil {
				return err
			}
		}
	} else if status == UserDisconnected || status == UserOffline {
		_, err = kv.PutString(s.ctx, userDisconnectedAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
		if err != nil {
			return err
		}
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
	if joinedAt, err := kv.Get(s.ctx, userJoinedAt); err == nil && joinedAt != nil {
		if parseUint, err := strconv.ParseUint(string(joinedAt.Value()), 10, 64); err == nil {
			info.JoinedAt = parseUint
		}
	}
	if reconnectedAt, err := kv.Get(s.ctx, userReconnectedAt); err == nil && reconnectedAt != nil {
		if parseUint, err := strconv.ParseUint(string(reconnectedAt.Value()), 10, 64); err == nil {
			info.ReconnectedAt = parseUint
		}
	}
	if disconnectedAt, err := kv.Get(s.ctx, userDisconnectedAt); err == nil && disconnectedAt != nil {
		if parseUint, err := strconv.ParseUint(string(disconnectedAt.Value()), 10, 64); err == nil {
			info.DisconnectedAt = parseUint
		}
	}

	return info, nil
}

func (s *NatsService) GetOnlineUsersList(roomId string) ([]*plugnmeet.NatsKvUserInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomUsers, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	kl, err := kv.ListKeys(s.ctx)
	if err != nil {
		return nil, err
	}

	var users []*plugnmeet.NatsKvUserInfo
	for k := range kl.Keys() {
		if status, err := kv.Get(s.ctx, k); err == nil && status != nil {
			if string(status.Value()) == UserOnline {
				info, err := s.GetUserInfo(k)
				if err == nil && info != nil {
					users = append(users, info)
				}
			}
		}
	}

	return users, nil
}

func (s *NatsService) GetOnlineUsersListAsJson(roomId string) ([]byte, error) {
	users, err := s.GetOnlineUsersList(roomId)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, nil
	}
	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	raw := make([]json.RawMessage, len(users))
	for i, u := range users {
		r, err := op.Marshal(u)
		if err != nil {
			return nil, err
		}
		raw[i] = r
	}

	return json.Marshal(raw)
}

// UpdateUserMetadata will basically update metadata only
// because normally we do not need to update other info
func (s *NatsService) UpdateUserMetadata(userId string, metadata *plugnmeet.UserMetadata) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	if err != nil {
		return "", err
	}

	// update id
	id := uuid.NewString()
	metadata.MetadataId = &id

	mt, err := s.MarshalParticipantMetadata(metadata)
	if err != nil {
		return "", err
	}

	_, err = kv.PutString(s.ctx, userMetadataKey, mt)
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
