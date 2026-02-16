package natsservice

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
)

type CachedRoomEntry struct {
	RoomInfo *plugnmeet.NatsKvRoomInfo
	stop     chan struct{}
}

type CachedUserInfoEntry struct {
	UserInfo        *plugnmeet.NatsKvUserInfo
	Status          string
	LastPingAt      uint64
	IsBlacklisted   bool
	TurnCredentials string
}

type NatsCacheService struct {
	// Global context for all long-lived watchers in this service
	serviceCtx    context.Context
	serviceCancel context.CancelFunc
	logger        *logrus.Entry

	// Single lock to protect all room-related cache maps
	roomLock           sync.RWMutex
	roomsInfoStore     map[string]CachedRoomEntry
	roomUsersInfoStore map[string]map[string]CachedUserInfoEntry
	roomFilesStore     map[string]map[string]*plugnmeet.RoomUploadedFileMetadata

	// Lock for the global recorder store
	recorderLock   sync.RWMutex
	recordersStore map[string]*utils.RecorderInfo
}

func newNatsCacheService(ctx context.Context, log *logrus.Entry) *NatsCacheService {
	ctx, cancel := context.WithCancel(ctx)
	return &NatsCacheService{
		serviceCtx:         ctx,
		serviceCancel:      cancel,
		roomsInfoStore:     make(map[string]CachedRoomEntry),
		roomUsersInfoStore: make(map[string]map[string]CachedUserInfoEntry),
		roomFilesStore:     make(map[string]map[string]*plugnmeet.RoomUploadedFileMetadata),
		recordersStore:     make(map[string]*utils.RecorderInfo),
		logger:             log.WithField("sub-service", "nats-cache"),
	}
}

func (ncs *NatsCacheService) convertTextToUint64(text string) uint64 {
	value, _ := strconv.ParseUint(text, 10, 64)
	return value
}

// addRoomWatcher will add a single "smart" watcher for the given consolidated room bucket.
func (ncs *NatsCacheService) addRoomWatcher(kv jetstream.KeyValue, bucket, roomId string) {
	log := ncs.logger.WithFields(logrus.Fields{
		"bucket": bucket,
		"room":   roomId,
	})

	// Acquire lock, do minimal work, release lock.
	ncs.roomLock.Lock()
	if _, ok := ncs.roomsInfoStore[roomId]; ok {
		ncs.roomLock.Unlock() // Release lock before returning
		return
	}

	// Initialize all cache stores for this room at once under the same lock.
	stopChan := make(chan struct{})
	ncs.roomsInfoStore[roomId] = CachedRoomEntry{
		RoomInfo: new(plugnmeet.NatsKvRoomInfo),
		stop:     stopChan,
	}
	ncs.roomUsersInfoStore[roomId] = make(map[string]CachedUserInfoEntry)
	ncs.roomFilesStore[roomId] = make(map[string]*plugnmeet.RoomUploadedFileMetadata)
	ncs.roomLock.Unlock() // RELEASE LOCK HERE, before network operation

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV smart watcher")
		// If watcher fails, clean up the entries we just created.
		// cleanRoomCache handles its own locking.
		ncs.cleanRoomCache(roomId)
		return
	}
	log.Infof("NATS KV smart watcher for room started")

	go func() {
		defer func() {
			log.Infof("NATS KV smart watcher for room stopped")
			_ = watcher.Stop()
			ncs.cleanRoomCache(roomId) // Clean up all related caches
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
					return // Channel closed
				}
				if entry != nil {
					ncs.dispatchCacheUpdate(entry, roomId)
				}
			}
		}
	}()
}

// dispatchCacheUpdate inspects the key and routes the update to the correct cache handler.
func (ncs *NatsCacheService) dispatchCacheUpdate(entry jetstream.KeyValueEntry, roomId string) {
	key := entry.Key()
	switch {
	case strings.HasPrefix(key, RoomInfoKeyPrefix):
		ncs.updateRoomInfoCache(entry, roomId)
	case strings.HasPrefix(key, UserKeyPrefix):
		// Use the new helper function to parse the user key
		userId, field, ok := ParseUserKey(key)
		if ok {
			ncs.updateUserInfoCache(entry, roomId, userId, field)
		} else {
			ncs.logger.WithFields(logrus.Fields{
				"key":    key,
				"roomId": roomId,
			}).Warn("failed to parse user key in dispatchCacheUpdate")
		}
	case strings.HasPrefix(key, FileKeyPrefix):
		fileId := strings.TrimPrefix(key, FileKeyPrefix)
		ncs.updateRoomFilesCache(entry, roomId, fileId)
	}
}

// watchRecorderKV will add a "smart" watcher for the global recorder info bucket.
func (ncs *NatsCacheService) watchRecorderKV(kv jetstream.KeyValue, log *logrus.Entry) {
	log = log.WithField("sub-service", "recorder-cache-watcher")

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV recorder watcher")
		return
	}
	log.Infof("NATS KV recorder watcher started for bucket: %s", kv.Bucket())

	go func() {
		defer func() {
			log.Infof("NATS KV recorder watcher stopped")
			_ = watcher.Stop()
		}()

		for {
			select {
			case <-ncs.serviceCtx.Done():
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					return // Channel closed
				}
				if entry != nil {
					ncs.updateRecorderCache(entry)
				}
			}
		}
	}()
}
