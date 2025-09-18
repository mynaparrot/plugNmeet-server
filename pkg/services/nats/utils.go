package natsservice

import (
	"errors"
	"strconv"

	"github.com/nats-io/nats.go/jetstream"
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
	if val, err := kv.Get(s.ctx, key); err == nil && val != nil {
		return string(val.Value()), nil
	}

	return "", nil
}

// getBoolValue retrieves a boolean value from the KeyValue store for the given key.
// Returns false if the key is not found or the value cannot be parsed as a boolean.
func (s *NatsService) getBoolValue(kv jetstream.KeyValue, key string) (bool, error) {
	val, err := s.getStringValue(kv, key)
	if err != nil {
		return false, err
	}
	if len(val) == 0 {
		return false, nil
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
	if len(val) == 0 {
		return 0, nil
	}
	return strconv.ParseUint(val, 10, 64)
}

// getInt64Value retrieves a int64 value from the KeyValue store for the given key.
// Returns 0 if the key is not found or the value cannot be parsed as a int64.
func (s *NatsService) getInt64Value(kv jetstream.KeyValue, key string) (int64, error) {
	val, err := s.getStringValue(kv, key)
	if err != nil {
		return 0, err
	}
	if len(val) == 0 {
		return 0, nil
	}
	return strconv.ParseInt(val, 10, 64)
}
