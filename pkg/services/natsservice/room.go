package natsservice

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"strconv"
	"time"
)

const (
	RoomInfoBucket     = Prefix + "roomInfo"
	roomIdKey          = "id"
	roomSidKey         = "sid"
	roomEnabledE2EEKey = "enabled_e2ee"
	roomMetadataKey    = "metadata"
	roomCreatedKey     = "created_at"
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

	mt, err := s.MarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	data := map[string]string{
		roomIdKey:          roomId,
		roomSidKey:         roomSid,
		roomEnabledE2EEKey: fmt.Sprintf("%v", metadata.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled),
		roomCreatedKey:     fmt.Sprintf("%d", time.Now().UnixMilli()),
		roomMetadataKey:    mt,
	}

	for k, v := range data {
		_, err = kv.PutString(s.ctx, k, v)
		if err != nil {
			log.Errorln(err)
		}
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

	info := new(plugnmeet.NatsKvRoomInfo)

	if id, err := kv.Get(s.ctx, roomIdKey); err == nil && id != nil {
		info.RoomId = string(id.Value())
	}
	if sid, err := kv.Get(s.ctx, roomSidKey); err == nil && sid != nil {
		info.RoomSid = string(sid.Value())
	}
	if enabledE2EE, err := kv.Get(s.ctx, roomEnabledE2EEKey); err == nil && enabledE2EE != nil {
		if val, err := strconv.ParseBool(string(enabledE2EE.Value())); err == nil {
			info.EnabledE2Ee = val
		}
	}
	if metadata, err := kv.Get(s.ctx, roomMetadataKey); err == nil && metadata != nil {
		info.Metadata = string(metadata.Value())
	}
	if createdAt, err := kv.Get(s.ctx, roomCreatedKey); err == nil && createdAt != nil {
		if parseUint, err := strconv.ParseUint(string(createdAt.Value()), 10, 64); err == nil {
			info.CreatedAt = parseUint
		}
	}

	return info, nil
}

func (s *NatsService) UpdateRoomMetadata(roomId string, metadata *plugnmeet.RoomMetadata) (string, error) {
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
