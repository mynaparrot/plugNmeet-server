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
	RoomInfoBucketPrefix = Prefix + "roomInfo-"
	RoomInfoBucket       = RoomInfoBucketPrefix + "%s"

	RoomDbTableIdKey    = "id"
	RoomIdKey           = "room_id"
	RoomSidKey          = "room_sid"
	RoomEmptyTimeoutKey = "empty_timeout"
	RoomStatusKey       = "status"
	RoomMetadataKey     = "metadata"
	RoomCreatedKey      = "created_at"

	RoomStatusCreated = "created"
	RoomStatusActive  = "active"
	RoomStatusEnded   = "ended"
)

func (s *NatsService) AddRoom(tableId uint64, roomId, roomSid string, emptyTimeout *uint32, metadata *plugnmeet.RoomMetadata) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   fmt.Sprintf(RoomInfoBucket, roomId),
	})
	if err != nil {
		return err
	}
	if emptyTimeout == nil {
		var et uint32 = 1800 // 1800 seconds = 30 minutes
		emptyTimeout = &et
	}

	mt, err := s.MarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	data := map[string]string{
		RoomDbTableIdKey:    fmt.Sprintf("%d", tableId),
		RoomIdKey:           roomId,
		RoomSidKey:          roomSid,
		RoomEmptyTimeoutKey: fmt.Sprintf("%d", *emptyTimeout),
		RoomStatusKey:       RoomStatusCreated,
		RoomCreatedKey:      fmt.Sprintf("%d", time.Now().UTC().Unix()), // in seconds
		RoomMetadataKey:     mt,
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

	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomInfoBucket, roomId))
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
	err := s.js.DeleteKeyValue(s.ctx, fmt.Sprintf(RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return nil
}

func (s *NatsService) UpdateRoomStatus(roomId string, status string) error {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf(RoomInfoBucket, roomId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return errors.New(fmt.Sprintf("no room found with roomId: %s", roomId))
	case err != nil:
		return err
	}

	_, err = kv.PutString(s.ctx, RoomStatusKey, status)
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) OnAfterSessionEndCleanup(roomId string) {
	err := s.DeleteRoom(roomId)
	if err != nil {
		log.Errorln(err)
	}

	err = s.DeleteAllRoomUsersWithConsumer(roomId)
	if err != nil {
		log.Errorln(err)
	}

	err = s.DeleteRoomNatsStream(roomId)
	if err != nil {
		log.Errorln(err)
	}
}
