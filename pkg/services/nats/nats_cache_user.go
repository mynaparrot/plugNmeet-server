package natsservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
	"strconv"
)

func (ncs *NatsCacheService) AddRoomUserStatusWatcher(kv jetstream.KeyValue, bucket, roomId, userId string) {
	ncs.userLock.Lock()
	rm, ok := ncs.roomUsersStatusStore[roomId]
	if !ok {
		ncs.roomUsersStatusStore[roomId] = make(map[string]CachedRoomUserStatusEntry)
		rm = ncs.roomUsersStatusStore[roomId]
	}
	_, ok = rm[userId]
	if ok {
		ncs.userLock.Unlock()
		return
	}
	ncs.userLock.Unlock()

	bucket += "." + userId
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
			ncs.cleanRoomUserStatusCache(roomId, userId)
		}()

		for {
			select {
			case <-ncs.serviceCtx.Done():
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					// channel closed may be bucket deleted
					return
				}
				if entry != nil && len(entry.Value()) > 0 {
					ncs.userLock.Lock()
					// force push updated data
					ncs.roomUsersStatusStore[roomId][userId] = CachedRoomUserStatusEntry{
						Status:   string(entry.Value()),
						Revision: entry.Revision(),
					}
					ncs.userLock.Unlock()
				}
			}
		}
	}()
}

func (ncs *NatsCacheService) GetCachedRoomUserStatus(roomId, userId string) (string, uint64) {
	ncs.userLock.RLock()
	defer ncs.userLock.RUnlock()
	if rm, found := ncs.roomUsersStatusStore[roomId]; found {
		if entry, ok := rm[userId]; ok {
			return entry.Status, entry.Revision
		}
	}
	return "", 0
}

func (ncs *NatsCacheService) GetUsersIdFromRoomStatusBucket(roomId, filterStatus string) []string {
	ncs.userLock.RLock()
	defer ncs.userLock.RUnlock()

	var usersIds []string
	if rm, found := ncs.roomUsersStatusStore[roomId]; found {
		for userId, val := range rm {
			if filterStatus != "" && val.Status == filterStatus {
				usersIds = append(usersIds, userId)
			} else if filterStatus == "" {
				// if no filter, return all users
				usersIds = append(usersIds, userId)
			}
		}
	}
	return usersIds
}

func (ncs *NatsCacheService) cleanRoomUserStatusCache(roomId, userId string) {
	ncs.userLock.Lock()
	defer ncs.userLock.Unlock()
	delete(ncs.roomUsersStatusStore[roomId], userId)
}

func (ncs *NatsCacheService) AddUserInfoWatcher(kv jetstream.KeyValue, bucket, roomId, userId string) {
	ncs.userLock.Lock()
	rm, ok := ncs.roomUsersInfoStore[roomId]
	if !ok {
		ncs.roomUsersInfoStore[roomId] = make(map[string]CachedUserInfoEntry)
		rm = ncs.roomUsersInfoStore[roomId]
	}
	_, ok = rm[userId]
	if ok {
		ncs.userLock.Unlock()
		return
	}
	ncs.userLock.Unlock()

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
			ncs.cleanUserInfoCache(roomId, userId)
		}()

		for {
			select {
			case <-ncs.serviceCtx.Done():
				return
			case entry, ok := <-watcher.Updates():
				if !ok {
					// channel closed may be bucket deleted
					return
				}
				if entry != nil && len(entry.Value()) > 0 {
					ncs.updateUserInfoCache(entry, roomId, userId)
				}
			}
		}
	}()
}

func (ncs *NatsCacheService) updateUserInfoCache(entry jetstream.KeyValueEntry, roomId, userId string) {
	ncs.userLock.Lock()
	defer ncs.userLock.Unlock()

	if _, ok := ncs.roomUsersInfoStore[roomId][userId]; !ok {
		ncs.roomUsersInfoStore[roomId][userId] = CachedUserInfoEntry{
			UserInfo: new(plugnmeet.NatsKvUserInfo),
		}
	}
	user := ncs.roomUsersInfoStore[roomId][userId]
	user.Revision = entry.Revision()

	val := string(entry.Value())
	switch entry.Key() {
	case UserIdKey:
		user.UserInfo.UserId = val
	case UserSidKey:
		user.UserInfo.UserSid = val
	case UserNameKey:
		user.UserInfo.Name = val
	case UserRoomIdKey:
		user.UserInfo.RoomId = val
	case UserMetadataKey:
		user.UserInfo.Metadata = val
	case UserIsAdminKey:
		user.UserInfo.IsAdmin, _ = strconv.ParseBool(val)
	case UserIsPresenterKey:
		user.UserInfo.IsPresenter, _ = strconv.ParseBool(val)
	case UserJoinedAt:
		user.UserInfo.JoinedAt = ncs.convertTextToUint64(val)
	case UserReconnectedAt:
		user.UserInfo.ReconnectedAt = ncs.convertTextToUint64(val)
	case UserDisconnectedAt:
		user.UserInfo.DisconnectedAt = ncs.convertTextToUint64(val)
	case UserLastPingAt:
		user.LastPingAt = ncs.convertTextToUint64(val)
	}
	// force push updated data
	ncs.roomUsersInfoStore[roomId][userId] = user
}

func (ncs *NatsCacheService) GetUserInfo(roomId, userId string) (*plugnmeet.NatsKvUserInfo, uint64) {
	ncs.userLock.RLock()
	defer ncs.userLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok && entry.UserInfo != nil {
			// Return a copy to prevent modification of cached object if it's a pointer
			infoCopy := proto.Clone(entry.UserInfo).(*plugnmeet.NatsKvUserInfo)
			return infoCopy, entry.Revision
		}
	}
	return nil, 0
}

func (ncs *NatsCacheService) GetUserLastPingAt(roomId, userId string) int64 {
	ncs.userLock.RLock()
	defer ncs.userLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok {
			return int64(entry.LastPingAt)
		}
	}
	return 0
}

func (ncs *NatsCacheService) cleanUserInfoCache(roomId, userId string) {
	ncs.userLock.Lock()
	defer ncs.userLock.Unlock()
	delete(ncs.roomUsersInfoStore[roomId], userId)
}
