package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"strconv"
	"time"
)

const (
	RoomInfoBucket  = Prefix + "roomInfo"
	roomIdKey       = "id"
	roomSidKey      = "sid"
	roomMetadataKey = "metadata"
	roomCreatedKey  = "created_at"
)

func (s *NatsService) CreateRoomNatsStreams(roomId string) error {
	_, err := s.js.CreateOrUpdateStream(s.ctx, jetstream.StreamConfig{
		Name: roomId,
		Subjects: []string{
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.ChatPublic),
			fmt.Sprintf("%s:%s.*.*", roomId, s.app.NatsInfo.Subjects.ChatPrivate),
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.SystemPublic),
			fmt.Sprintf("%s:%s.*.*", roomId, s.app.NatsInfo.Subjects.SystemPrivate),
			fmt.Sprintf("%s:%s.*", roomId, s.app.NatsInfo.Subjects.Whiteboard),
		},
	})
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) AddRoom(roomId, roomSid string, metadata *plugnmeet.RoomMetadata) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Bucket: fmt.Sprintf("%s-%s", RoomInfoBucket, roomId),
	})
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, roomIdKey, roomId)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, roomSidKey, roomSid)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, roomCreatedKey, fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		return err
	}

	mt, err := s.MarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	_, err = kv.PutString(s.ctx, roomMetadataKey, mt)
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) GetRoomInfo(roomId string) (*plugnmeet.NatsKvRoomInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	id, _ := kv.Get(s.ctx, roomIdKey)
	sid, _ := kv.Get(s.ctx, roomSidKey)
	metadata, _ := kv.Get(s.ctx, roomMetadataKey)

	info := &plugnmeet.NatsKvRoomInfo{
		RoomId:   string(id.Value()),
		RoomSid:  string(sid.Value()),
		Metadata: string(metadata.Value()),
	}
	createdAt, _ := kv.Get(s.ctx, roomCreatedKey)
	if parseUint, err := strconv.ParseUint(string(createdAt.Value()), 10, 64); err == nil {
		info.CreatedAt = parseUint
	}

	return info, nil
}

func (s *NatsService) UpdateRoom(roomId string, metadata *plugnmeet.RoomMetadata) (string, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
	if err != nil {
		return "", err
	}

	// update id
	id := uuid.NewString()
	metadata.MetadataId = &id

	mt, err := s.MarshalRoomMetadata(metadata)
	if err != nil {
		return "", err
	}

	_, err = kv.PutString(s.ctx, roomMetadataKey, mt)
	if err != nil {
		return "", err
	}

	return mt, nil
}

func (s *NatsService) DeleteRoom(roomId string) error {
	err := s.js.DeleteKeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return nil
}
