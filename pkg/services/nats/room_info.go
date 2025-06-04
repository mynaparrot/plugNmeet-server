package natsservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
)

// GetRoomInfo retrieves the room information for the given roomId
func (s *NatsService) GetRoomInfo(roomId string) (*plugnmeet.NatsKvRoomInfo, error) {
	kv, err := s.getKV(fmt.Sprintf(RoomInfoBucket, roomId))
	if err != nil || kv == nil {
		return nil, err
	}

	info := new(plugnmeet.NatsKvRoomInfo)
	info.DbTableId, _ = s.getUint64Value(kv, RoomDbTableIdKey)
	info.RoomId, _ = s.getStringValue(kv, RoomIdKey)
	info.RoomSid, _ = s.getStringValue(kv, RoomSidKey)
	info.Status, _ = s.getStringValue(kv, RoomStatusKey)
	info.EmptyTimeout, _ = s.getUint64Value(kv, RoomEmptyTimeoutKey)
	info.MaxParticipants, _ = s.getUint64Value(kv, RoomMaxParticipants)
	info.Metadata, _ = s.getStringValue(kv, RoomMetadataKey)
	info.CreatedAt, _ = s.getUint64Value(kv, RoomCreatedKey)

	return info, nil
}

// GetRoomInfoWithMetadata retrieves the room information along with metadata for the given roomId
func (s *NatsService) GetRoomInfoWithMetadata(roomId string) (*plugnmeet.NatsKvRoomInfo, *plugnmeet.RoomMetadata, error) {
	info, err := s.GetRoomInfo(roomId)
	if err != nil || info == nil {
		return nil, nil, err
	}

	metadata, err := s.UnmarshalRoomMetadata(info.Metadata)
	if err != nil {
		return nil, nil, err
	}

	return info, metadata, nil
}

// GetRoomKeyValue retrieves the value for the given key from the room's KeyValue store
func (s *NatsService) GetRoomKeyValue(roomId, key string) (jetstream.KeyValueEntry, error) {
	kv, err := s.getKV(fmt.Sprintf(RoomInfoBucket, roomId))
	if err != nil || kv == nil {
		return nil, err
	}

	val, err := kv.Get(s.ctx, key)
	if err != nil {
		return nil, err
	}

	return val, nil
}

// GetRoomMetadataStruct retrieves the room metadata as a structured object for the given roomId
func (s *NatsService) GetRoomMetadataStruct(roomId string) (*plugnmeet.RoomMetadata, error) {
	metadata, err := s.GetRoomKeyValue(roomId, RoomMetadataKey)
	if err != nil || metadata == nil {
		return nil, err
	}

	if len(metadata.Value()) == 0 {
		return nil, nil
	}

	return s.UnmarshalRoomMetadata(string(metadata.Value()))
}

// GetRoomStatus retrieves the room status for the given roomId
func (s *NatsService) GetRoomStatus(roomId string) (string, error) {
	value, err := s.GetRoomKeyValue(roomId, RoomStatusKey)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return string(value.Value()), nil
}
