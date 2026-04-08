package natsservice

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/proto"
)

// setRoomInfoCache is the single entry point for updating the room info cache.
// It can be called either by the NATS watcher or manually.
func (ncs *NatsCacheService) setRoomInfoCache(roomId, field, value string, revision uint64) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()

	cacheEntry, ok := ncs.roomsInfoStore[roomId]
	if !ok {
		// This can happen if the room was cleaned up just after the event was dispatched.
		return
	}

	// If the incoming revision is older than what we have, ignore it.
	if revision > 0 && cacheEntry.LastRevision >= revision {
		return
	}

	switch field {
	case RoomDbTableIdKey:
		cacheEntry.RoomInfo.DbTableId = ncs.convertTextToUint64(value)
	case RoomIdKey:
		cacheEntry.RoomInfo.RoomId = value
	case RoomSidKey:
		cacheEntry.RoomInfo.RoomSid = value
	case RoomStatusKey:
		cacheEntry.RoomInfo.Status = value
		if value == RoomStatusEnded {
			ncs.cleanRoomCacheUnsafe(roomId)
			return // Important to return here as the cache entry is gone
		}
	case RoomEmptyTimeoutKey:
		cacheEntry.RoomInfo.EmptyTimeout = ncs.convertTextToUint64(value)
	case RoomMaxParticipants:
		cacheEntry.RoomInfo.MaxParticipants = ncs.convertTextToUint64(value)
	case RoomCreatedKey:
		cacheEntry.RoomInfo.CreatedAt = ncs.convertTextToUint64(value)
	case RoomMetadataKey:
		cacheEntry.RoomInfo.Metadata = value
	}

	cacheEntry.LastRevision = revision
	ncs.roomsInfoStore[roomId] = cacheEntry
}

func (ncs *NatsCacheService) getCachedRoomInfo(roomID string) *plugnmeet.NatsKvRoomInfo {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if cachedEntry, found := ncs.roomsInfoStore[roomID]; found && cachedEntry.RoomInfo != nil {
		if cachedEntry.RoomInfo.DbTableId == 0 || cachedEntry.RoomInfo.Status == RoomStatusEnded {
			return nil
		}
		infoCopy := proto.Clone(cachedEntry.RoomInfo).(*plugnmeet.NatsKvRoomInfo)
		return infoCopy
	}
	return nil
}

// getCachedRoomMetadata retrieves only the metadata string from the cache.
// It returns the metadata and a boolean indicating if it was found.
func (ncs *NatsCacheService) getCachedRoomMetadata(roomID string) (string, bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if cachedEntry, found := ncs.roomsInfoStore[roomID]; found && cachedEntry.RoomInfo != nil {
		if cachedEntry.RoomInfo.DbTableId == 0 || cachedEntry.RoomInfo.Status == RoomStatusEnded {
			return "", false // Treat ended rooms as not found
		}
		return cachedEntry.RoomInfo.Metadata, true
	}
	return "", false
}

func (ncs *NatsCacheService) cleanRoomCache(roomID string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()
	ncs.cleanRoomCacheUnsafe(roomID)
}

// cleanRoomCacheUnsafe performs the cleanup for all caches related to a room.
// The caller MUST hold the lock before calling this.
func (ncs *NatsCacheService) cleanRoomCacheUnsafe(roomID string) {
	if entry, ok := ncs.roomsInfoStore[roomID]; ok {
		close(entry.stop)
	}
	delete(ncs.roomsInfoStore, roomID)
	delete(ncs.roomUsersInfoStore, roomID)
	delete(ncs.roomFilesStore, roomID)
}
