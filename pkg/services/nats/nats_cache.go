package natsservice

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
)

var (
	defaultNatsCacheService *NatsCacheService
	initCacheOnce           sync.Once
)

type CachedRoomEntry struct {
	RoomInfo *plugnmeet.NatsKvRoomInfo
	stop     chan struct{}
}

type CachedUserInfoEntry struct {
	UserInfo   *plugnmeet.NatsKvUserInfo
	Status     string
	LastPingAt uint64
}

type NatsCacheService struct {
	// Global context for all long-lived watchers in this service
	serviceCtx    context.Context
	serviceCancel context.CancelFunc
	logger        *logrus.Entry

	roomLock       sync.RWMutex
	roomsInfoStore map[string]CachedRoomEntry

	roomUsersInfoLock  sync.RWMutex
	roomUsersInfoStore map[string]map[string]CachedUserInfoEntry
}

func InitNatsCacheService(app *config.AppConfig, log *logrus.Logger) {
	initCacheOnce.Do(func() {
		if app.JetStream == nil {
			log.Fatal("NATS JetStream not provided to InitNatsCacheService")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defaultNatsCacheService = &NatsCacheService{
			serviceCtx:         ctx,
			serviceCancel:      cancel,
			roomsInfoStore:     make(map[string]CachedRoomEntry),
			roomUsersInfoStore: make(map[string]map[string]CachedUserInfoEntry),
			logger:             log.WithField("sub-service", "nats-cache"),
		}
	})
}

// GetNatsCacheService returns the singleton instance.
func GetNatsCacheService(app *config.AppConfig, logger *logrus.Logger) *NatsCacheService {
	if defaultNatsCacheService == nil {
		InitNatsCacheService(app, logger)
	}
	return defaultNatsCacheService
}

// Shutdown gracefully stops all watchers.
func (ncs *NatsCacheService) Shutdown() {
	ncs.logger.Info("Shutting down NATS Cache Service...")
	ncs.serviceCancel() // Signals all watchers started with ncs.serviceCtx to stop
	ncs.logger.Info("NATS Cache Service shutdown complete.")
}

func (ncs *NatsCacheService) convertTextToUint64(text string) uint64 {
	value, _ := strconv.ParseUint(text, 10, 64)
	return value
}

// AddRoomWatcher will add a single "smart" watcher for the given consolidated room bucket.
func (ncs *NatsCacheService) AddRoomWatcher(kv jetstream.KeyValue, bucket, roomId string) {
	log := ncs.logger.WithFields(logrus.Fields{
		"bucket": bucket,
		"room":   roomId,
	})

	ncs.roomLock.Lock()
	if _, ok := ncs.roomsInfoStore[roomId]; ok {
		ncs.roomLock.Unlock()
		return // Already watching
	}

	// Initialize all cache stores for this room at once.
	stopChan := make(chan struct{})
	ncs.roomsInfoStore[roomId] = CachedRoomEntry{
		RoomInfo: new(plugnmeet.NatsKvRoomInfo),
		stop:     stopChan,
	}
	ncs.roomUsersInfoStore[roomId] = make(map[string]CachedUserInfoEntry)
	ncs.roomLock.Unlock()

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV smart watcher")
		ncs.cleanRoomCache(roomId) // Clean up all related caches
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
	case strings.HasPrefix(key, UserKeyUserIdPrefix):
		// parsing based on the "user-USERID_<userId>-FIELD_<field>" schema.
		trimmed := strings.TrimPrefix(key, UserKeyUserIdPrefix)
		parts := strings.SplitN(trimmed, UserKeyFieldPrefix, 2)

		if len(parts) == 2 {
			userId := parts[0]
			field := parts[1]
			ncs.updateUserInfoCache(entry, roomId, userId, field)
		}
	}
}
