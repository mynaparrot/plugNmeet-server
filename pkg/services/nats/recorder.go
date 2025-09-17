package natsservice

import (
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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
	kv, err := s.getKV(fmt.Sprintf("%s-%s", s.app.NatsInfo.Recorder.RecorderInfoKv, recorderId))
	if err != nil || kv == nil {
		return nil, err
	}

	info := &RecorderInfo{
		RecorderId: recorderId,
	}
	info.MaxLimit, _ = s.getInt64Value(kv, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_MAX_LIMIT))
	info.CurrentProgress, _ = s.getInt64Value(kv, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_CURRENT_PROGRESS))
	info.LastPing, _ = s.getInt64Value(kv, fmt.Sprintf("%d", plugnmeet.RecorderInfoKeys_RECORDER_INFO_LAST_PING))

	return info, nil
}
