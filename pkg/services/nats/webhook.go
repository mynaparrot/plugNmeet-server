package natsservice

import (
	"errors"

	"github.com/nats-io/nats.go/jetstream"
)

const WebhookKvKey = Prefix + "webhookData"
const WebhookCleanupSubject = Prefix + "webhookCleanup"

func (s *NatsService) AddWebhookData(roomId string, val []byte) error {
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   WebhookKvKey,
	})
	if err != nil {
		return err
	}

	_, err = kv.Put(s.ctx, roomId, val)
	if err != nil {
		return err
	}

	return nil
}

func (s *NatsService) GetWebhookData(roomId string) ([]byte, error) {
	kv, err := s.js.KeyValue(s.ctx, WebhookKvKey)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	entry, err := kv.Get(s.ctx, roomId)
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	if entry == nil {
		return nil, nil
	}

	return entry.Value(), nil
}

func (s *NatsService) DeleteWebhookData(roomId string) error {
	kv, err := s.js.KeyValue(s.ctx, WebhookKvKey)
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil
	case err != nil:
		return err
	}

	return kv.Purge(s.ctx, roomId)
}
