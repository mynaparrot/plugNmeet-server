package natsservice

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"strconv"
	"strings"
	"time"
)

type RecorderInfo struct {
	RecorderId      string
	MaxLimit        int64
	CurrentProgress int64
	LastPing        int64
}

func (s *NatsService) GetAllActiveRecorders() []*RecorderInfo {
	kl := s.app.JetStream.KeyValueStoreNames(s.ctx)
	knm := fmt.Sprintf("%s-", s.app.NatsInfo.Recorder.RecorderInfoKv)
	var rrs []*RecorderInfo
	valid := time.Now().UnixMilli() - (8 * 1000) // we can think maximum 8-second delay for a valid node

	for name := range kl.Name() {
		if strings.HasPrefix(name, knm) {
			recorderId := strings.ReplaceAll(name, knm, "")
			if info, err := s.GetRecorderInfo(recorderId); err == nil && info != nil {
				if info.LastPing >= valid {
					rrs = append(rrs, info)
				}
			}
		}
	}

	return rrs
}

func (s *NatsService) GetRecorderInfo(recorderId string) (*RecorderInfo, error) {
	kv, err := s.js.KeyValue(s.ctx, fmt.Sprintf("%s-%s", s.app.NatsInfo.Recorder.RecorderInfoKv, recorderId))
	switch {
	case errors.Is(err, jetstream.ErrBucketNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}

	info := &RecorderInfo{
		RecorderId: recorderId,
	}

	if maxLimit, err := kv.Get(s.ctx, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_MAX_LIMIT)); err == nil && maxLimit != nil {
		if parseUint, err := strconv.ParseInt(string(maxLimit.Value()), 10, 64); err == nil {
			info.MaxLimit = parseUint
		}
	}
	if currentPro, err := kv.Get(s.ctx, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_CURRENT_PROGRESS)); err == nil && currentPro != nil {
		if parseUint, err := strconv.ParseInt(string(currentPro.Value()), 10, 64); err == nil {
			info.CurrentProgress = parseUint
		}
	}
	if lastPing, err := kv.Get(s.ctx, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_LAST_PING)); err == nil && lastPing != nil {
		if parseUint, err := strconv.ParseInt(string(lastPing.Value()), 10, 64); err == nil {
			info.LastPing = parseUint
		}
	}

	return info, nil
}
