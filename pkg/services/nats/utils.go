package natsservice

import (
	"errors"
	"github.com/nats-io/nats.go/jetstream"
	"strconv"
)

// getKV retrieves the KeyValue store for the given bucket name.
// Returns nil if the bucket is not found.
func (s *NatsService) getKV(bucket string) (jetstream.KeyValue, error) {
	kv, err := s.js.KeyValue(s.ctx, bucket)
	if errors.Is(err, jetstream.ErrBucketNotFound) {
		return nil, nil
	}
	return kv, err
}

// getStringValue retrieves a string value from the KeyValue store for the given key.
// Returns an empty string if the key is not found.
func (s *NatsService) getStringValue(kv jetstream.KeyValue, key string) (string, error) {
	val, err := kv.Get(s.ctx, key)
	if err != nil || val == nil {
		return "", err
	}
	return string(val.Value()), nil
}

// getBoolValue retrieves a boolean value from the KeyValue store for the given key.
// Returns false if the key is not found or the value cannot be parsed as a boolean.
func (s *NatsService) getBoolValue(kv jetstream.KeyValue, key string) (bool, error) {
	val, err := s.getStringValue(kv, key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(val)
}

// getUint64Value retrieves a uint64 value from the KeyValue store for the given key.
// Returns 0 if the key is not found or the value cannot be parsed as a uint64.
func (s *NatsService) getUint64Value(kv jetstream.KeyValue, key string) (uint64, error) {
	val, err := s.getStringValue(kv, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(val, 10, 64)
}

func (s *NatsService) getInt64Value(kv jetstream.KeyValue, key string) (int64, error) {
	val, err := s.getStringValue(kv, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}
