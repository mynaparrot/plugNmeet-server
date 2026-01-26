package natsservice

import (
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

func (ncs *NatsCacheService) updateRoomInfoCache(entry jetstream.KeyValueEntry, roomId string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()

	cacheEntry, ok := ncs.roomsInfoStore[roomId]
	if !ok {
		return
	}

	val := string(entry.Value())
	field := strings.TrimPrefix(entry.Key(), RoomInfoKeyPrefix)

	switch field {
	case RoomDbTableIdKey:
		cacheEntry.RoomInfo.DbTableId = ncs.convertTextToUint64(val)
	case RoomIdKey:
		cacheEntry.RoomInfo.RoomId = val
	case RoomSidKey:
		cacheEntry.RoomInfo.RoomSid = val
	case RoomStatusKey:
		cacheEntry.RoomInfo.Status = val
		if val == RoomStatusEnded {
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
	ncs.roomsInfoStore[roomId] = cacheEntry
}

func (ncs *NatsCacheService) getCachedRoomInfo(roomID string) *plugnmeet.NatsKvRoomInfo {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()
	if cachedEntry, found := ncs.roomsInfoStore[roomID]; found && cachedEntry.RoomInfo != nil {
		if cachedEntry.RoomInfo.Status == RoomStatusEnded {
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
		if cachedEntry.RoomInfo.Status == RoomStatusEnded {
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

	// Also clean the unified user info cache for this room.
	ncs.roomUsersInfoLock.Lock()
	delete(ncs.roomUsersInfoStore, roomID)
	ncs.roomUsersInfoLock.Unlock()
}
