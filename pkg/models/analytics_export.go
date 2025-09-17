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
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *AnalyticsModel) PrepareToExportAnalytics(roomId, sid, meta string) {
	if m.app.AnalyticsSettings == nil || !m.app.AnalyticsSettings.Enabled {
		return
	}

	// if no metadata then it is hard to make next logics
	// if still there was some data stored in redis,
	// we will have to think different way to clean those
	if meta == "" || sid == "" {
		return
	}

	metadata, err := m.natsService.UnmarshalRoomMetadata(meta)
	if err != nil {
		return
	}

	// let's wait a few seconds so that all other processes will finish
	time.Sleep(config.WaitBeforeAnalyticsStartProcessing)

	// we'll check if the room is still active or not.
	// this may happen when we closed the room & re-created it instantly
	exist, err := m.natsService.GetRoomInfo(roomId)
	if err == nil && exist != nil {
		m.logger.Infoln("this room:", roomId, "still active or created again, so we won't process to export analytics")
		return
	}

	// lock to prevent this room re-creation until process finish
	// otherwise will give an unexpected result
	if lockValue, err := acquireRoomCreationLockWithRetry(context.Background(), m.rs, roomId, m.logger); err == nil {
		defer func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if unlockErr := m.rs.UnlockRoomCreation(unlockCtx, roomId, lockValue); unlockErr != nil {
				// UnlockRoomCreation in RedisService should log details
				m.logger.Errorf("Error trying to clean up room creation lock for room %s : %v", roomId, unlockErr)
			}
		}()
	}

	if _, err := os.Stat(*m.app.AnalyticsSettings.FilesStorePath); os.IsNotExist(err) {
		err = os.MkdirAll(*m.app.AnalyticsSettings.FilesStorePath, os.ModePerm)
		if err != nil {
			m.logger.Errorln(err)
			return
		}
	}

	isRunning := 0
	room, _ := m.ds.GetRoomInfoBySid(sid, &isRunning)
	if room == nil || room.ID == 0 {
		return
	}

	fileId := fmt.Sprintf("%s-%d", room.Sid, room.CreationTime)
	path := fmt.Sprintf("%s/%s.json", *m.app.AnalyticsSettings.FilesStorePath, fileId)

	// export file
	stat, err := m.exportAnalyticsToFile(room, path, metadata)
	if err != nil {
		m.logger.Errorln(err)
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
			m.logger.Errorln(err)
		}
		// notify
		go m.sendToWebhookNotifier(room.RoomId, room.Sid, "analytics_proceeded", fileId)
	}
}

func (m *AnalyticsModel) exportAnalyticsToFile(room *dbmodels.RoomInfo, path string, metadata *plugnmeet.RoomMetadata) (os.FileInfo, error) {
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
		m.logger.Errorf("failed to scan analytics keys for room %s: %v", room.RoomId, err)
		return nil, err
	}
	if len(allKeys) == 0 {
		m.logger.Infof("no analytics keys found for room %s, skipping file generation for empty analytics", room.RoomId)
	}

	// Process room-level events
	roomKeyPrefix := fmt.Sprintf(analyticsRoomKey+":room", room.RoomId)
	for _, key := range allKeys {
		if strings.HasPrefix(key, roomKeyPrefix) && !strings.Contains(key, ":user:") {
			m.processEventKey(key, roomKeyPrefix, &roomInfo.Events)
		}
	}

	var usersInfo []*plugnmeet.AnalyticsUserInfo

	// get users first
	k := fmt.Sprintf("%s:users", roomKeyPrefix)
	users, err := m.rs.AnalyticsGetAllUsers(k)
	if err != nil {
		m.logger.Errorln(err)
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
		userKeyPrefix := fmt.Sprintf(analyticsUserKey, room.RoomId, i)
		for _, key := range allKeys {
			if strings.HasPrefix(key, userKeyPrefix) {
				m.processEventKey(key, userKeyPrefix, &userInfo.Events)
			}
		}

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
			m.logger.Errorln(err)
			return nil, err
		}

		err = os.WriteFile(path, marshal, 0644)
		if err != nil {
			m.logger.Errorln(err)
			return nil, err
		}
		stat, err = os.Stat(path)
		if err != nil {
			m.logger.Errorln(err)
			return nil, err
		}

	}

	// at the end delete all redis records
	// also add the users key to the deletion list
	usersKey := fmt.Sprintf(analyticsRoomKey+":room:users", room.RoomId)
	allKeys = append(allKeys, usersKey)

	if err = m.rs.AnalyticsDeleteKeys(allKeys); err != nil {
		m.logger.Errorln(err)
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
	n := helpers.GetWebhookNotifier(m.app, m.logger.Logger)
	if n != nil {
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

		err := n.SendWebhookEvent(msg)
		if err != nil {
			m.logger.Errorln(err)
		}
	}
}
