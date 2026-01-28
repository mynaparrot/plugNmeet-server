package natsservice

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go/jetstream"
)

func (s *NatsService) createRecorderKV() {
	_, err := s.js.CreateOrUpdateKeyValue(s.ctx, jetstream.KeyValueConfig{
		Replicas: s.app.NatsInfo.NumReplicas,
		Bucket:   s.app.NatsInfo.Recorder.RecorderInfoKv,
	})
	if err != nil {
		s.logger.WithError(err).Fatalf("could not create recorder info bucket")
	}
	s.logger.Infof("successfully created recorder info bucket: %s", s.app.NatsInfo.Recorder.RecorderInfoKv)
}

func (s *NatsService) GetAllActiveRecorders() []*utils.RecorderInfo {
	kv, err := s.getKV(s.app.NatsInfo.Recorder.RecorderInfoKv)
	if err != nil {
		s.logger.WithError(err).Warnln("could not get recorder info bucket")
		return nil
	}

	keys, err := kv.ListKeys(s.ctx)
	if err != nil {
		s.logger.WithError(err).Warnln("could not list keys in recorder info bucket")
		return nil
	}

	// Group keys by recorderId
	recorderKeys := make(map[string]map[string]string)
	for key := range keys.Keys() {
		// Use the new utils function
		recorderId, field, ok := utils.ParseRecorderKey(key)
		if !ok {
			continue
		}
		if _, exists := recorderKeys[recorderId]; !exists {
			recorderKeys[recorderId] = make(map[string]string)
		}
		entry, err := kv.Get(s.ctx, key)
		if err == nil {
			recorderKeys[recorderId][field] = string(entry.Value())
		}
	}

	var rrs []*utils.RecorderInfo
	valid := time.Now().UnixMilli() - (8 * 1000) // 8-second delay tolerance

	// Define field names for clarity
	maxLimitField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_MAX_LIMIT)
	progressField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_CURRENT_PROGRESS)
	lastPingField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_LAST_PING)

	for id, fields := range recorderKeys {
		lastPing, _ := strconv.ParseInt(fields[lastPingField], 10, 64)
		if lastPing < valid {
			continue // Skip inactive recorder
		}

		maxLimit, _ := strconv.ParseInt(fields[maxLimitField], 10, 64)
		currentProgress, _ := strconv.ParseInt(fields[progressField], 10, 64)

		rrs = append(rrs, &utils.RecorderInfo{
			RecorderId:      id,
			MaxLimit:        maxLimit,
			CurrentProgress: currentProgress,
			LastPing:        lastPing,
		})
	}

	return rrs
}

func (s *NatsService) GetRecorderInfo(recorderId string) (*utils.RecorderInfo, error) {
	kv, err := s.getKV(s.app.NatsInfo.Recorder.RecorderInfoKv)
	if err != nil {
		return nil, err
	}

	info := &utils.RecorderInfo{
		RecorderId: recorderId,
	}

	// Define field names for clarity
	maxLimitField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_MAX_LIMIT)
	progressField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_CURRENT_PROGRESS)
	lastPingField := fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_LAST_PING)

	// Use the new utils function
	maxLimitKey := utils.FormatRecorderKey(recorderId, maxLimitField)
	progressKey := utils.FormatRecorderKey(recorderId, progressField)
	pingKey := utils.FormatRecorderKey(recorderId, lastPingField)

	info.MaxLimit, _ = s.getInt64Value(kv, maxLimitKey)
	info.CurrentProgress, _ = s.getInt64Value(kv, progressKey)
	info.LastPing, _ = s.getInt64Value(kv, pingKey)

	// If LastPing is 0, it means the keys were not found, so the recorder doesn't exist.
	if info.LastPing == 0 {
		return nil, nil
	}

	return info, nil
}
