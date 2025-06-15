package natsservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func (ncs *NatsCacheService) AddRoomWatcher(kv jetstream.KeyValue, bucket, roomId string) {
	ncs.roomLock.Lock()
	_, ok := ncs.roomsInfoStore[roomId]
	if ok {
		ncs.roomLock.Unlock()
		return
	}
	ncs.roomLock.Unlock()

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.Errorln(fmt.Sprintf("Error starting NATS KV watcher for %s: %v", bucket, err))
		return
	}
	log.Infof("NATS KV watcher started for bucket: %s", bucket)

	go func() {
		defer func() {
			log.Infof("NATS KV watcher for %s stopped.", bucket)
			_ = watcher.Stop()
			ncs.cleanRoomCache(roomId)
		}()

		for {
			select {
			case <-ncs.serviceCtx.Done():
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

	if _, ok := ncs.roomsInfoStore[roomId]; !ok {
		ncs.roomsInfoStore[roomId] = CachedRoomEntry{
			RoomInfo: new(plugnmeet.NatsKvRoomInfo),
		}
	}
	cacheEntry := ncs.roomsInfoStore[roomId]
	cacheEntry.Revision = entry.Revision()

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

func (ncs *NatsCacheService) GetCachedRoomInfo(roomID string) (*plugnmeet.NatsKvRoomInfo, uint64) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if cachedEntry, found := ncs.roomsInfoStore[roomID]; found && cachedEntry.RoomInfo != nil {
		metaCopy := proto.Clone(cachedEntry.RoomInfo).(*plugnmeet.NatsKvRoomInfo)
		return metaCopy, cachedEntry.Revision
	}
	return nil, 0
}

func (ncs *NatsCacheService) cleanRoomCache(roomID string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()
	delete(ncs.roomsInfoStore, roomID)
}
