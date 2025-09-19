package natsservice

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// AddRoomWatcher will add a watcher for the given roomId in the NATS KV store.
// each room will have its own watcher
func (ncs *NatsCacheService) AddRoomWatcher(kv jetstream.KeyValue, bucket, roomId string) {
	log := ncs.logger.WithFields(logrus.Fields{
		"bucket": bucket,
		"room":   roomId,
	})

	ncs.roomLock.Lock()
	_, ok := ncs.roomsInfoStore[roomId]
	if ok {
		//already watching this room
		ncs.roomLock.Unlock()
		return
	}
	// Create a stop channel for this watcher.
	stopChan := make(chan struct{})
	ncs.roomsInfoStore[roomId] = CachedRoomEntry{
		RoomInfo: new(plugnmeet.NatsKvRoomInfo),
		stop:     stopChan,
	}
	ncs.roomLock.Unlock()

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV watcher")
		// fallback to clean cache as we've set it above
		ncs.cleanRoomCache(roomId)
		return
	}
	log.Infof("NATS KV watcher for room started")

	go func() {
		defer func() {
			log.Infof("NATS KV watcher for room stopped")
			_ = watcher.Stop()
			ncs.cleanRoomCache(roomId)
		}()

		for {
			select {
			case <-ncs.serviceCtx.Done():
				return
			case <-stopChan:
				log.Info("Explicit stop signal received")
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					// Channel closed may be bucket deleted
					return
				}
				if entry != nil && len(entry.Value()) > 0 {
					ncs.updateRoomCache(entry, roomId)
				}
			}
		}
	}()
}

func (ncs *NatsCacheService) updateRoomCache(entry jetstream.KeyValueEntry, roomId string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()

	cacheEntry, ok := ncs.roomsInfoStore[roomId]
	if !ok {
		// Entry was cleaned up by another process.
		return
	}

	val := string(entry.Value())
	switch entry.Key() {
	case RoomDbTableIdKey:
		cacheEntry.RoomInfo.DbTableId = ncs.convertTextToUint64(val)
	case RoomIdKey:
		cacheEntry.RoomInfo.RoomId = val
	case RoomSidKey:
		cacheEntry.RoomInfo.RoomSid = val
	case RoomStatusKey:
		cacheEntry.RoomInfo.Status = val
		if val == RoomStatusEnded {
			// The room has ended. We can clean up now since we already have the lock.
			ncs.cleanRoomCacheUnsafe(roomId)
			return
		}
	case RoomEmptyTimeoutKey:
		cacheEntry.RoomInfo.EmptyTimeout = ncs.convertTextToUint64(val)
	case RoomMaxParticipants:
		cacheEntry.RoomInfo.MaxParticipants = ncs.convertTextToUint64(val)
	case RoomCreatedKey:
		cacheEntry.RoomInfo.CreatedAt = ncs.convertTextToUint64(val)
	case RoomMetadataKey:
		cacheEntry.RoomInfo.Metadata = val
	}
	// force push updated data
	ncs.roomsInfoStore[roomId] = cacheEntry
}

func (ncs *NatsCacheService) GetCachedRoomInfo(roomID string) *plugnmeet.NatsKvRoomInfo {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if cachedEntry, found := ncs.roomsInfoStore[roomID]; found && cachedEntry.RoomInfo != nil {
		if cachedEntry.RoomInfo.Status == RoomStatusEnded {
			// don't deliver cache value if room has ended status
			return nil
		}
		infoCopy := proto.Clone(cachedEntry.RoomInfo).(*plugnmeet.NatsKvRoomInfo)
		return infoCopy
	}
	return nil
}

func (ncs *NatsCacheService) cleanRoomCache(roomID string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()
	ncs.cleanRoomCacheUnsafe(roomID)
}

// cleanRoomCacheUnsafe performs the cleanup without acquiring a lock.
// The caller MUST hold the lock before calling this.
func (ncs *NatsCacheService) cleanRoomCacheUnsafe(roomID string) {
	// Check if the entry exists before trying to stop its watcher.
	if entry, ok := ncs.roomsInfoStore[roomID]; ok {
		// Signal the watcher goroutine to stop.
		close(entry.stop)
	}
	delete(ncs.roomsInfoStore, roomID)
}
