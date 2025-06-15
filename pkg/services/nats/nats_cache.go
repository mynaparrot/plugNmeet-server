package natsservice

import (
	"context"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"strconv"
	"sync"
)

var (
	defaultNatsCacheService *NatsCacheService
	initCacheOnce           sync.Once
)

type CachedRoomEntry struct {
	RoomInfo *plugnmeet.NatsKvRoomInfo
	Revision uint64
}

type CachedRoomUserStatusEntry struct {
	Status   string
	Revision uint64
}

type CachedUserInfoEntry struct {
	UserInfo   *plugnmeet.NatsKvUserInfo
	LastPingAt uint64
	Revision   uint64
}

type NatsCacheService struct {
	// Global context for all long-lived watchers in this service
	serviceCtx    context.Context
	serviceCancel context.CancelFunc

	roomLock       sync.RWMutex
	roomsInfoStore map[string]CachedRoomEntry

	userLock             sync.RWMutex
	roomUsersStatusStore map[string]map[string]CachedRoomUserStatusEntry
	roomUsersInfoStore   map[string]map[string]CachedUserInfoEntry
}

func InitNatsCacheService(app *config.AppConfig) {
	initCacheOnce.Do(func() {
		if app == nil {
			app = config.GetConfig()
		}
		if app.JetStream == nil {
			log.Fatal("NATS JetStream not provided to InitNatsCacheService")
		}

		ctx, cancel := context.WithCancel(context.Background())
		defaultNatsCacheService = &NatsCacheService{
			serviceCtx:           ctx,
			serviceCancel:        cancel,
			roomsInfoStore:       make(map[string]CachedRoomEntry),
			roomUsersStatusStore: make(map[string]map[string]CachedRoomUserStatusEntry),
			roomUsersInfoStore:   make(map[string]map[string]CachedUserInfoEntry),
		}
	})
}

// GetNatsCacheService returns the singleton instance.
func GetNatsCacheService() *NatsCacheService {
	if defaultNatsCacheService == nil {
		log.Fatal("NatsCacheService not initialized. Call InitNatsCacheService at startup.")
	}
	return defaultNatsCacheService
}

// Shutdown gracefully stops all watchers.
func (ncs *NatsCacheService) Shutdown() {
	log.Info("Shutting down NATS Cache Service...")
	ncs.serviceCancel() // Signals all watchers started with ncs.serviceCtx to stop
	log.Info("NATS Cache Service shutdown complete.")
}

func (ncs *NatsCacheService) convertTextToUint64(text string) uint64 {
	value, _ := strconv.ParseUint(text, 10, 64)
	return value
}
