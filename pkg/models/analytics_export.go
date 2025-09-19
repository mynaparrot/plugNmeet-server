package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

// PrepareToExportAnalytics will export analytics data and create file
// this method is the final call after proper delay
func (m *AnalyticsModel) PrepareToExportAnalytics(roomId, sid, meta string) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    roomId,
		"roomSid":   sid,
		"operation": "ExportAnalytics",
	})
	if m.app.AnalyticsSettings == nil || !m.app.AnalyticsSettings.Enabled {
		log.Debug("analytics is disabled, skipping export")
		return
	}

	// if no metadata then it is hard to make next logics
	// if still there was some data stored in redis,
	// we will have to think different way to clean those
	if meta == "" || sid == "" {
		log.Warn("metadata or sid is empty, skipping analytics export")
		return
	}

	metadata, err := m.natsService.UnmarshalRoomMetadata(meta)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal room metadata")
		return
	}

	// lock to prevent this room re-creation until process finish
	// otherwise will give an unexpected result
	lockValue, err := acquireRoomCreationLockWithRetry(m.ctx, m.rs, roomId, log)
	if err != nil {
		// Error is already logged by the helper.
		// We can't proceed without the lock.
		return
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		if unlockErr := m.rs.UnlockRoomCreation(unlockCtx, roomId, lockValue); unlockErr != nil {
			// UnlockRoomCreation in RedisService should log details
			log.WithError(unlockErr).Error("error trying to clean up room creation lock")
		} else {
			log.Info("room creation lock released")
		}
	}()

	// we'll check if the room is still active or not.
	// this may happen when we closed the room & re-created it instantly
	exist, err := m.natsService.GetRoomInfo(roomId)
	if err == nil && exist != nil && exist.RoomSid != sid {
		log.Info("room was likely re-created, skipping analytics export for the previous session")
		return // The lock will be released by the deferred function.
	}

	isRunning := 0
	room, err := m.ds.GetRoomInfoBySid(sid, &isRunning)
	if err != nil {
		log.WithError(err).Error("failed to get room info from db")
	}
	if room == nil || room.ID == 0 {
		log.Warn("could not find ended room in db, skipping analytics export")
		return
	}

	fileId := fmt.Sprintf("%s-%d", room.Sid, room.CreationTime)
	path := fmt.Sprintf("%s/%s.json", *m.app.AnalyticsSettings.FilesStorePath, fileId)

	stat, err := m.exportAnalyticsToFile(room, path, metadata, log)
	if err != nil {
		log.WithError(err).Error("failed to export analytics to file")
		return
	}

	// it's not possible to get room metadata as always
	// so, if room didn't have activated analytics feature,
	// we will simply won't create the in exportAnalyticsToFile method
	// and won't record to DB
	if metadata.RoomFeatures.EnableAnalytics {
		// record in db
		_, err = m.AddAnalyticsFileToDB(room.ID, room.CreationTime, room.RoomId, fileId, stat)
		if err != nil {
			log.WithError(err).Error("failed to add analytics file to db")
		}
		// notify
		go m.sendToWebhookNotifier(room.RoomId, room.Sid, "analytics_proceeded", fileId)
	} else {
		log.Debug("analytics feature was not enabled for this room, file not saved to DB")
	}
}

func (m *AnalyticsModel) exportAnalyticsToFile(room *dbmodels.RoomInfo, path string, metadata *plugnmeet.RoomMetadata, log *logrus.Entry) (os.FileInfo, error) {
	roomInfo := &plugnmeet.AnalyticsRoomInfo{
		RoomId:       room.RoomId,
		RoomTitle:    room.RoomTitle,
		RoomCreation: room.Created.Unix(),
		RoomEnded:    room.Ended.Unix(),
		EnabledE2Ee:  metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetIsEnabled(),
		Events:       []*plugnmeet.AnalyticsEventData{},
	}

	// Use SCAN (via Keys) to find all analytics keys for this room
	scanPattern := fmt.Sprintf(analyticsRoomKey, room.RoomId) + ":*"
	allKeys, err := m.rs.AnalyticsScanKeys(scanPattern)
	if err != nil {
		log.WithError(err).Error("failed to scan analytics keys for room")
		return nil, err
	}
	if len(allKeys) == 0 {
		log.Info("no analytics keys found, file will contain only basic room info")
	} else {
		log.Infof("found %d total analytics keys to process", len(allKeys))
	}

	// Process room-level events
	roomKeyPrefix := fmt.Sprintf(analyticsRoomKey+":room", room.RoomId)
	var roomKeysProcessed int
	for _, key := range allKeys {
		if strings.HasPrefix(key, roomKeyPrefix) && !strings.Contains(key, ":user:") {
			m.processEventKey(key, roomKeyPrefix, &roomInfo.Events)
			roomKeysProcessed++
		}
	}
	log.Infof("processed %d room-level analytics keys", roomKeysProcessed)

	var usersInfo []*plugnmeet.AnalyticsUserInfo

	// get users first
	k := fmt.Sprintf("%s:users", roomKeyPrefix)
	users, err := m.rs.AnalyticsGetAllUsers(k)
	if err != nil {
		log.WithError(err).Error("failed to get analytics users from redis")
		return nil, err
	}
	roomInfo.RoomTotalUsers = int64(len(users))
	roomInfo.RoomDuration = roomInfo.RoomEnded - roomInfo.RoomCreation

	// now users related events
	for i, n := range users {
		uf := new(plugnmeet.AnalyticsRedisUserInfo)
		_ = protojson.Unmarshal([]byte(n), uf)
		userInfo := &plugnmeet.AnalyticsUserInfo{
			UserId:   i,
			Name:     *uf.Name,
			IsAdmin:  uf.IsAdmin,
			ExUserId: uf.ExUserId,
			Events:   []*plugnmeet.AnalyticsEventData{},
		}

		// Process user-level events
		var userKeysProcessed int
		userKeyPrefix := fmt.Sprintf(analyticsUserKey, room.RoomId, i)
		for _, key := range allKeys {
			if strings.HasPrefix(key, userKeyPrefix) {
				m.processEventKey(key, userKeyPrefix, &userInfo.Events)
				userKeysProcessed++
			}
		}

		log.WithField("user_id", i).Infof("processed %d analytics keys for user", userKeysProcessed)
		usersInfo = append(usersInfo, userInfo)
	}

	var stat os.FileInfo
	// it's not possible to get room metadata as always
	// so, if room didn't have activated analytics feature,
	// we will simply won't create the file & delete all records
	if metadata.RoomFeatures.EnableAnalytics {
		result := &plugnmeet.AnalyticsResult{
			Room:  roomInfo,
			Users: usersInfo,
		}
		op := protojson.MarshalOptions{
			EmitUnpopulated: true,
			UseProtoNames:   true,
		}
		marshal, err := op.Marshal(result)
		if err != nil {
			log.WithError(err).Error("failed to marshal analytics result")
			return nil, err
		}

		err = os.WriteFile(path, marshal, 0644)
		if err != nil {
			log.WithError(err).Error("failed to write analytics file")
			return nil, err
		}
		stat, err = os.Stat(path)
		if err != nil {
			log.WithError(err).Error("failed to stat new analytics file")
			return nil, err
		}
	}

	// at the end delete all redis records
	// also add the users key to the deletion list
	usersKey := fmt.Sprintf(analyticsRoomKey+":room:users", room.RoomId)
	allKeys = append(allKeys, usersKey)

	if err = m.rs.AnalyticsDeleteKeys(allKeys); err != nil {
		log.WithError(err).Error("failed to delete analytics keys from redis")
	}

	return stat, err
}

func (m *AnalyticsModel) processEventKey(key, prefix string, eventList *[]*plugnmeet.AnalyticsEventData) {
	// Extract event name from the key, e.g., "ANALYTICS_EVENT_ROOM_POLL_ADDED"
	eventNameWithPrefix := strings.TrimPrefix(key, prefix+":")
	eventName := ""

	if strings.HasPrefix(eventNameWithPrefix, "ANALYTICS_EVENT_ROOM_") {
		eventName = strings.ToLower(strings.Replace(eventNameWithPrefix, "ANALYTICS_EVENT_ROOM_", "", 1))
	} else if strings.HasPrefix(eventNameWithPrefix, "ANALYTICS_EVENT_USER_") {
		eventName = strings.ToLower(strings.Replace(eventNameWithPrefix, "ANALYTICS_EVENT_USER_", "", 1))
	} else {
		return // Not a valid event key format we want to process.
	}

	eventInfo := &plugnmeet.AnalyticsEventData{
		Name:  eventName,
		Total: 0,
	}

	if err := m.buildEventInfo(key, eventInfo); err == nil {
		*eventList = append(*eventList, eventInfo)
	}
}

func (m *AnalyticsModel) buildEventInfo(ekey string, eventInfo *plugnmeet.AnalyticsEventData) error {
	// we'll check type first
	rType, err := m.rs.AnalyticsGetKeyType(ekey)
	if err != nil || rType == "none" {
		// Key doesn't exist or there was an error, which is fine. Just skip it.
		return fmt.Errorf("key %s not found or error getting type: %w", ekey, err)
	}
	if rType == "hash" {
		var evals []*plugnmeet.AnalyticsEventValue
		result, err := m.rs.GetAnalyticsAllHashTypeVals(ekey)
		if err != nil {
			m.logger.Errorln(err)
			return err
		}
		for kk, rv := range result {
			tt, _ := strconv.ParseInt(kk, 10, 64)
			val := &plugnmeet.AnalyticsEventValue{
				Time:  tt,
				Value: rv,
			}
			evals = append(evals, val)
		}
		eventInfo.Total = uint32(len(result))
		eventInfo.Values = evals
	} else {
		result, err := m.rs.GetAnalyticsStringTypeVal(ekey)
		if !errors.Is(err, redis.Nil) && err != nil {
			m.logger.Println(err)
			return err
		}
		if result != "" {
			c, err := strconv.Atoi(result)
			if err != nil {
				// we are assuming that we want to get the value as it
				eventInfo.Total = 1
				val := &plugnmeet.AnalyticsEventValue{
					Value: result,
				}
				eventInfo.Values = append(eventInfo.Values, val)
			} else {
				eventInfo.Total = uint32(c)
			}
		}
	}
	return nil
}

func (m *AnalyticsModel) sendToWebhookNotifier(roomId, roomSid, task, fileId string) {
	if m.webhookNotifier != nil {
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &task,
			Room: &plugnmeet.NotifyEventRoom{
				Sid:    &roomSid,
				RoomId: &roomId,
			},
			Analytics: &plugnmeet.AnalyticsEvent{
				FileId: &fileId,
			},
		}

		err := m.webhookNotifier.SendWebhookEvent(msg)
		if err != nil {
			m.logger.Errorln(err)
		}
	}
}
