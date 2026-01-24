package natsservice

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// GetRoomInfo retrieves the room information for the given roomId from the consolidated bucket
func (s *NatsService) GetRoomInfo(roomId string) (*plugnmeet.NatsKvRoomInfo, error) {
	// try to get cached room info first
	if info := s.cs.GetCachedRoomInfo(roomId); info != nil {
		return info, nil
	}

	bucket := s.formatConsolidatedRoomBucket(roomId)
	kv, err := s.getKV(bucket)
	if err != nil || kv == nil {
		return nil, err
	}

	info := new(plugnmeet.NatsKvRoomInfo)
	info.DbTableId, _ = s.getUint64Value(kv, s.formatRoomKey(RoomDbTableIdKey))
	info.RoomId, _ = s.getStringValue(kv, s.formatRoomKey(RoomIdKey))
	info.RoomSid, _ = s.getStringValue(kv, s.formatRoomKey(RoomSidKey))
	info.Status, _ = s.getStringValue(kv, s.formatRoomKey(RoomStatusKey))
	info.EmptyTimeout, _ = s.getUint64Value(kv, s.formatRoomKey(RoomEmptyTimeoutKey))
	info.MaxParticipants, _ = s.getUint64Value(kv, s.formatRoomKey(RoomMaxParticipants))
	info.CreatedAt, _ = s.getUint64Value(kv, s.formatRoomKey(RoomCreatedKey))
	info.Metadata, _ = s.getStringValue(kv, s.formatRoomKey(RoomMetadataKey))

	// So, for some reason, if the room info is not found in cache,
	// then may be room wasn't created in this server.
	// So, we will start watching if status not ended
	if info.Status != RoomStatusEnded {
		s.cs.AddRoomWatcher(kv, bucket, roomId)
	}

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

// GetRoomMetadataStruct retrieves the room metadata as a structured object for the given roomId
func (s *NatsService) GetRoomMetadataStruct(roomId string) (*plugnmeet.RoomMetadata, error) {
	info, err := s.GetRoomInfo(roomId)
	if err != nil {
		return nil, err
	}

	if info == nil || len(info.Metadata) == 0 {
		return nil, nil
	}

	return s.UnmarshalRoomMetadata(info.Metadata)
}
