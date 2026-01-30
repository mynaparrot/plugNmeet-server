package natsservice

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// updateRoomFilesCache is called by the smart watcher dispatcher to update the file cache.
func (ncs *NatsCacheService) updateRoomFilesCache(entry jetstream.KeyValueEntry, roomId, fileId string) {
	ncs.roomLock.Lock()
	defer ncs.roomLock.Unlock()

	roomFiles, roomOk := ncs.roomFilesStore[roomId]
	if !roomOk {
		// This can happen if the room was cleaned up just after the event was dispatched.
		return
	}

	// Handle deletion
	if entry.Operation() == jetstream.KeyValueDelete || entry.Operation() == jetstream.KeyValuePurge {
		delete(roomFiles, fileId)
		return
	}

	// Handle PUT (add/update)
	meta := new(plugnmeet.RoomUploadedFileMetadata)
	err := protojson.Unmarshal(entry.Value(), meta)
	if err != nil {
		ncs.logger.WithError(err).Errorln("failed to unmarshal file metadata for cache")
		return
	}

	roomFiles[fileId] = meta
}

// getCachedRoomFile retrieves a specific file's metadata from the cache.
func (ncs *NatsCacheService) getCachedRoomFile(roomId, fileId string) (*plugnmeet.RoomUploadedFileMetadata, bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()

	if roomFiles, ok := ncs.roomFilesStore[roomId]; ok {
		if file, ok := roomFiles[fileId]; ok {
			// Use proto.Clone to return a deep copy and prevent race conditions.
			fileCopy := proto.Clone(file).(*plugnmeet.RoomUploadedFileMetadata)
			return fileCopy, true
		}
	}
	return nil, false
}

// getAllCachedRoomFiles retrieves all file metadata for a given room from the cache.
func (ncs *NatsCacheService) getAllCachedRoomFiles(roomId string) (map[string]*plugnmeet.RoomUploadedFileMetadata, bool) {
	ncs.roomLock.RLock()
	defer ncs.roomLock.RUnlock()

	if roomFiles, ok := ncs.roomFilesStore[roomId]; ok {
		// Return a deep copy of the map and its values to prevent race conditions.
		copiedFiles := make(map[string]*plugnmeet.RoomUploadedFileMetadata, len(roomFiles))
		for k, v := range roomFiles {
			fileCopy := proto.Clone(v).(*plugnmeet.RoomUploadedFileMetadata)
			copiedFiles[k] = fileCopy
		}
		return copiedFiles, true
	}

	return nil, false
}
