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
	RoomInfoBucket     = Prefix + "roomInfo"
	RoomIdKey          = "id"
	RoomSidKey         = "sid"
	RoomEnabledE2EEKey = "enabled_e2ee"
	RoomMetadataKey    = "metadata"
	RoomCreatedKey     = "created_at"
)

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
		RoomIdKey:          roomId,
		RoomSidKey:         roomSid,
		RoomEnabledE2EEKey: fmt.Sprintf("%v", metadata.RoomFeatures.EndToEndEncryptionFeatures.IsEnabled),
		RoomCreatedKey:     fmt.Sprintf("%d", time.Now().UnixMilli()),
		RoomMetadataKey:    mt,
	}

	for k, v := range data {
		_, err = kv.PutString(s.ctx, k, v)
		if err != nil {
			log.Errorln(err)
		}
	}

	return nil
}

// updateRoomMetadata should be internal only
func (s *NatsService) updateRoomMetadata(roomId string, metadata interface{}) (string, error) {
	var mt *plugnmeet.RoomMetadata
	var err error

	switch v := metadata.(type) {
	case string:
		// because we'll need to update id
		mt, err = s.UnmarshalRoomMetadata(v)
		if err != nil {
			return "", err
		}
	case plugnmeet.RoomMetadata:
		mt = &v
	case *plugnmeet.RoomMetadata:
		mt = v
	default:
		return "", errors.New("invalid metadata data type")
	}

	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return "", errors.New(fmt.Sprintf("no room found with roomId: %s", roomId))
	case err != nil:
		return "", err
	}

	// id will be updated during Marshal
	ml, err := s.MarshalRoomMetadata(mt)
	if err != nil {
		return "", err
	}

	_, err = kv.PutString(s.ctx, RoomMetadataKey, ml)
	if err != nil {
		return "", err
	}

	return ml, nil
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
