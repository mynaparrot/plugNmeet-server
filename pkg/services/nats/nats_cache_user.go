package natsservice

import (
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

// AddRoomUserStatusWatcher will start watching user status in a specific room.
// remember each room has only one RoomUsersBucket bucket
// in this bucket userId is key and status is value
func (ncs *NatsCacheService) AddRoomUserStatusWatcher(kv jetstream.KeyValue, bucket, roomId string) {
	log := ncs.logger.WithFields(logrus.Fields{
		"bucket": bucket,
		"room":   roomId,
	})

	ncs.userLock.Lock()
	_, ok := ncs.roomUsersStatusStore[roomId]
	if ok {
		//already watching this room
		ncs.userLock.Unlock()
		return
	}
	ncs.roomUsersStatusStore[roomId] = make(map[string]CachedRoomUserStatusEntry)
	ncs.userLock.Unlock()

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV watcher")
		// fallback to clean cache as we've set it above
		ncs.cleanRoomUserStatusCache(roomId)
		return
	}
	log.Infof("NATS KV watcher for room user status started")

	go func() {
		defer func() {
			log.Infof("NATS KV watcher for room user status stopped")
			_ = watcher.Stop()
			ncs.cleanRoomUserStatusCache(roomId)
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
				// here each user id is a separate key and status is the value
				if entry != nil {
					ncs.userLock.Lock()
					// force push updated data
					ncs.roomUsersStatusStore[roomId][entry.Key()] = CachedRoomUserStatusEntry{
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

// remember each room has only one RoomUsersBucket bucket
// in this bucket userId is key and status is value
func (ncs *NatsCacheService) cleanRoomUserStatusCache(roomId string) {
	ncs.userLock.Lock()
	defer ncs.userLock.Unlock()
	delete(ncs.roomUsersStatusStore, roomId)
}

// AddUserInfoWatcher will start watching user info
// each user has its own bucket, so watch should be for each userId
func (ncs *NatsCacheService) AddUserInfoWatcher(kv jetstream.KeyValue, bucket, roomId, userId string) {
	log := ncs.logger.WithFields(logrus.Fields{
		"bucket": bucket,
		"room":   roomId,
		"user":   userId,
	})

	ncs.userLock.Lock()
	rm, ok := ncs.roomUsersInfoStore[roomId]
	if !ok {
		ncs.roomUsersInfoStore[roomId] = make(map[string]CachedUserInfoEntry)
		rm = ncs.roomUsersInfoStore[roomId]
	}
	_, ok = rm[userId]
	if ok {
		// already watching user info for this userId
		ncs.userLock.Unlock()
		return
	}
	ncs.roomUsersInfoStore[roomId][userId] = CachedUserInfoEntry{
		UserInfo: new(plugnmeet.NatsKvUserInfo),
	}
	ncs.userLock.Unlock()

	opts := []jetstream.WatchOpt{jetstream.IncludeHistory()}
	watcher, err := kv.WatchAll(ncs.serviceCtx, opts...)
	if err != nil {
		log.WithError(err).Errorln("Error starting NATS KV watcher")
		// fallback to clean cache as we've set it above
		ncs.cleanUserInfoCache(roomId, userId)
		return
	}
	log.Infof("NATS KV watcher for user started")

	go func() {
		defer func() {
			log.Infof("NATS KV watcher for user info stopped")
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
	user := ncs.roomUsersInfoStore[roomId][userId]

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

func (ncs *NatsCacheService) GetUserInfo(roomId, userId string) *plugnmeet.NatsKvUserInfo {
	ncs.userLock.RLock()
	defer ncs.userLock.RUnlock()
	if rm, found := ncs.roomUsersInfoStore[roomId]; found {
		if entry, ok := rm[userId]; ok && entry.UserInfo != nil {
			// Return a copy to prevent modification of cached object if it's a pointer
			infoCopy := proto.Clone(entry.UserInfo).(*plugnmeet.NatsKvUserInfo)
			return infoCopy
		}
	}
	return nil
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
