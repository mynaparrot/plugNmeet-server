package natsservice

import (
	"strconv"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go/jetstream"
)

// updateRecorderCache is called by the recorder watcher to update the global recorder cache.
func (ncs *NatsCacheService) updateRecorderCache(entry jetstream.KeyValueEntry) {
	ncs.recorderLock.Lock()
	defer ncs.recorderLock.Unlock()

	recorderId, field, ok := utils.ParseRecorderKey(entry.Key())
	if !ok {
		return
	}

	// Handle deletion of the entire recorder entry
	if entry.Operation() == jetstream.KeyValuePurge {
		// Only log and delete if the entry actually still exists in our cache.
		if _, exists := ncs.recordersStore[recorderId]; exists {
			ncs.logger.Infof("recorder %s went offline, removing from local cache", recorderId)
			delete(ncs.recordersStore, recorderId)
		}
		return
	}

	// Get or create the recorder info in the cache
	recorder, exists := ncs.recordersStore[recorderId]
	if !exists {
		ncs.logger.Infof("adding recorder %s to local cache", recorderId)
		recorder = &utils.RecorderInfo{RecorderId: recorderId}
		ncs.recordersStore[recorderId] = recorder
	}

	val := string(entry.Value())
	fieldKey, _ := strconv.Atoi(field)

	switch plugnmeet.RecorderInfoKeys(fieldKey) {
	case plugnmeet.RecorderInfoKeys_RECORDER_INFO_MAX_LIMIT:
		recorder.MaxLimit, _ = strconv.ParseInt(val, 10, 64)
	case plugnmeet.RecorderInfoKeys_RECORDER_INFO_CURRENT_PROGRESS:
		recorder.CurrentProgress, _ = strconv.ParseInt(val, 10, 64)
	case plugnmeet.RecorderInfoKeys_RECORDER_INFO_LAST_PING:
		recorder.LastPing, _ = strconv.ParseInt(val, 10, 64)
	}
}

// getAllCachedActiveRecorders retrieves all active recorders from the cache.
func (ncs *NatsCacheService) getAllCachedActiveRecorders(pingTimeout time.Duration) []*utils.RecorderInfo {
	ncs.recorderLock.RLock()
	defer ncs.recorderLock.RUnlock()

	recorders := make([]*utils.RecorderInfo, 0, len(ncs.recordersStore))

	// Calculate the cutoff time. Any ping before this time is considered stale.
	cutoffTime := time.Now().UTC().Add(-pingTimeout)

	for _, r := range ncs.recordersStore {
		// Convert the stored UnixMilli timestamp to a time.Time object.
		pingTime := time.UnixMilli(r.LastPing)

		// Use time.After() for a clear and safe comparison.
		if pingTime.After(cutoffTime) {
			// Create a copy to avoid race conditions if the caller modifies it
			infoCopy := *r
			recorders = append(recorders, &infoCopy)
		}
	}

	return recorders
}

// getCachedRecorderInfo retrieves a specific recorder's info from the cache.
func (ncs *NatsCacheService) getCachedRecorderInfo(recorderId string) (*utils.RecorderInfo, bool) {
	ncs.recorderLock.RLock()
	defer ncs.recorderLock.RUnlock()

	if recorder, ok := ncs.recordersStore[recorderId]; ok {
		// Create a copy to avoid race conditions
		infoCopy := *recorder
		return &infoCopy, true
	}

	return nil, false
}
