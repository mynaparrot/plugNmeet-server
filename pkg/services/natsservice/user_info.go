package natsservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"strconv"
)

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

func (s *NatsService) GetUserInfo(userId string) (*plugnmeet.NatsKvUserInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	info := new(plugnmeet.NatsKvUserInfo)

	if id, err := kv.Get(s.ctx, UserIdKey); err == nil && id != nil {
		info.UserId = string(id.Value())
	}
	if sid, err := kv.Get(s.ctx, UserSidKey); err == nil && sid != nil {
		info.UserSid = string(sid.Value())
	}
	if name, err := kv.Get(s.ctx, UserNameKey); err == nil && name != nil {
		info.Name = string(name.Value())
	}
	if roomId, err := kv.Get(s.ctx, UserRoomIdKey); err == nil && roomId != nil {
		info.RoomId = string(roomId.Value())
	}
	if metadata, err := kv.Get(s.ctx, UserMetadataKey); err == nil && metadata != nil {
		info.Metadata = string(metadata.Value())
	}
	if isAdmin, err := kv.Get(s.ctx, UserIsAdminKey); err == nil && isAdmin != nil {
		if val, err := strconv.ParseBool(string(isAdmin.Value())); err == nil {
			info.IsAdmin = val
		}
	}
	if isPresenter, err := kv.Get(s.ctx, UserIsPresenterKey); err == nil && isPresenter != nil {
		if val, err := strconv.ParseBool(string(isPresenter.Value())); err == nil {
			info.IsPresenter = val
		}
	}
	if joinedAt, err := kv.Get(s.ctx, UserJoinedAt); err == nil && joinedAt != nil {
		if parseUint, err := strconv.ParseUint(string(joinedAt.Value()), 10, 64); err == nil {
			info.JoinedAt = parseUint
		}
	}
	if reconnectedAt, err := kv.Get(s.ctx, UserReconnectedAt); err == nil && reconnectedAt != nil {
		if parseUint, err := strconv.ParseUint(string(reconnectedAt.Value()), 10, 64); err == nil {
			info.ReconnectedAt = parseUint
		}
	}
	if disconnectedAt, err := kv.Get(s.ctx, UserDisconnectedAt); err == nil && disconnectedAt != nil {
		if parseUint, err := strconv.ParseUint(string(disconnectedAt.Value()), 10, 64); err == nil {
			info.DisconnectedAt = parseUint
		}
	}

	return info, nil
}

func (s *NatsService) GetRoomAllUsersFromStatusBucket(roomId string) (map[string]jetstream.KeyValueEntry, error) {
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

	users := make(map[string]jetstream.KeyValueEntry)
	for k := range kl.Keys() {
		if entry, err := kv.Get(s.ctx, k); err == nil && entry != nil {
			users[k] = entry
		}
	}

	return users, nil
}

func (s *NatsService) GetOlineUsersId(roomId string) ([]string, error) {
	users, err := s.GetRoomAllUsersFromStatusBucket(roomId)
	if err != nil {
		return nil, err
	}
	if users == nil || len(users) == 0 {
		return nil, errors.New("no user found")
	}

	var userIds []string
	for id, entry := range users {
		if string(entry.Value()) == UserOnline {
			userIds = append(userIds, id)
		}
	}

	return userIds, nil
}

func (s *NatsService) GetOnlineUsersList(roomId string) ([]*plugnmeet.NatsKvUserInfo, error) {
	userIds, err := s.GetOlineUsersId(roomId)
	if err != nil {
		return nil, err
	}
	if userIds == nil || len(userIds) == 0 {
		return nil, errors.New("no user found")
	}

	var users []*plugnmeet.NatsKvUserInfo
	for _, id := range userIds {
		info, err := s.GetUserInfo(id)
		if err == nil && info != nil {
			users = append(users, info)
		}
	}

	return users, nil
}

func (s *NatsService) GetOnlineUsersListAsJson(roomId string) ([]byte, error) {
	users, err := s.GetOnlineUsersList(roomId)
	if err != nil {
		return nil, err
	}
	if users == nil || len(users) == 0 {
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

func (s *NatsService) GetUserKeyValue(userId, key string) (jetstream.KeyValueEntry, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", UserInfoBucket, userId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	val, err := kv.Get(s.ctx, key)
	if err != nil {
		return nil, err
	}

	return val, nil
}
