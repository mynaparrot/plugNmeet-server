package natsservice

import (
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go/jetstream"
)

// createRecorderKVAndWatch ensures the recorder KV bucket exists and starts the watcher.
func (s *NatsService) createRecorderKVAndWatch() {
	bucket := s.app.NatsInfo.Recorder.RecorderInfoKv
	kv, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   bucket,
	})
	if err != nil {
		s.logger.WithError(err).Fatalf("could not create recorder info bucket %s", bucket)
	}
	s.logger.Infof("successfully created recorder info bucket: %s", bucket)

	// Now that the bucket exists, tell the cache service to start watching it.
	s.cs.watchRecorderKV(kv, s.logger)
}

// GetAllActiveRecorders retrieves all active recorders directly from the local cache.
func (s *NatsService) GetAllActiveRecorders() []*utils.RecorderInfo {
	return s.cs.getAllCachedActiveRecorders(s.app.RecorderInfo.PingTimeout)
}

// GetRecorderInfo retrieves a specific recorder's info directly from the local cache.
func (s *NatsService) GetRecorderInfo(recorderId string) (*utils.RecorderInfo, error) {
	if recorder, ok := s.cs.getCachedRecorderInfo(recorderId); ok {
		return recorder, nil
	}
	return nil, nil
}
