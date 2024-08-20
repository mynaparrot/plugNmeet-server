package natsservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"strconv"
)

func (s *NatsService) GetRoomInfo(roomId string) (*plugnmeet.NatsKvRoomInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	info := new(plugnmeet.NatsKvRoomInfo)

	if id, err := kv.Get(s.ctx, RoomIdKey); err == nil && id != nil {
		info.RoomId = string(id.Value())
	}
	if sid, err := kv.Get(s.ctx, RoomSidKey); err == nil && sid != nil {
		info.RoomSid = string(sid.Value())
	}
	if emptyTimeout, err := kv.Get(s.ctx, RoomEmptyTimeoutKey); err == nil && emptyTimeout != nil {
		if parseUint, err := strconv.ParseUint(string(emptyTimeout.Value()), 10, 64); err == nil {
			info.EmptyTimeout = parseUint
		}
	}
	if enabledE2EE, err := kv.Get(s.ctx, RoomEnabledE2EEKey); err == nil && enabledE2EE != nil {
		if val, err := strconv.ParseBool(string(enabledE2EE.Value())); err == nil {
			info.EnabledE2Ee = val
		}
	}
	if metadata, err := kv.Get(s.ctx, RoomMetadataKey); err == nil && metadata != nil {
		info.Metadata = string(metadata.Value())
	}
	if createdAt, err := kv.Get(s.ctx, RoomCreatedKey); err == nil && createdAt != nil {
		if parseUint, err := strconv.ParseUint(string(createdAt.Value()), 10, 64); err == nil {
			info.CreatedAt = parseUint // in seconds
		}
	}

	return info, nil
}

func (s *NatsService) GetRoomInfoWithMetadata(roomId string) (*plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata, error) {
	info, err := s.GetRoomInfo(roomId)
	if err != nil {
		return nil, nil, err
	}
	if info == nil {
		return nil, nil, nil
	}
	metadata, err := s.UnmarshalRoomMetadata(info.Metadata)
	if err != nil {
		return nil, nil, err
	}

	return info, metadata, nil
}

func (s *NatsService) GetRoomKeyValue(roomId, key string) (jetstream.KeyValueEntry, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
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

func (s *NatsService) GetRoomMetadataStruct(roomId string) (*plugnmeet.RoomMetadata, error) {
	metadata, err := s.GetRoomKeyValue(roomId, RoomMetadataKey)
	if err != nil {
		return nil, err
	}

	if metadata == nil || len(metadata.Value()) == 0 {
		return nil, nil
	}

	return s.UnmarshalRoomMetadata(string(metadata.Value()))
}
