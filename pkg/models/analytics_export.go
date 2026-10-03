package models

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

// ExportAnalytics exports analytics for an ended session. The caller must already hold the room-creation lock.
func (m *AnalyticsModel) ExportAnalytics(roomId, sid, meta string, endedAt time.Time) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":    roomId,
		"roomSid":   sid,
		"operation": "ExportAnalytics",
	})

	defer m.webhookNotifier.DeleteWebhook(roomId, log)

	if m.app.AnalyticsSettings == nil || !m.app.AnalyticsSettings.Enabled {
		log.Debug("Analytics is disabled, skipping export")
		return
	}

	// If metadata is missing, it becomes difficult to proceed with the next logic.
	// If any stale data still exists in Redis, it will be cleaned up by the TTL.
	if meta == "" || sid == "" {
		log.Warn("Metadata or sid is empty, skipping analytics export")
		return
	}

	metadata, err := m.natsService.UnmarshalRoomMetadata(meta)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal room metadata")
		return
	}

	room, err := m.ds.GetRoomInfoBySid(sid, new(0))
	if err != nil {
		log.WithError(err).Error("failed to get room info from db")
	}
	if room == nil || room.ID == 0 {
		log.Warn("could not find ended room in db, skipping analytics export")
		return
	}

	// synthesize a left event for users still connected at room end
	m.markStillConnectedUsersAsLeft(room, endedAt, log)

	jsonData, err := m.exportAnalyticsToJSON(room, metadata, log)
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
		artifact, err := m.artifactModel.CreateAnalyticsArtifact(room.ID, jsonData, log)
		if err != nil {
			log.WithError(err).Error("failed to create analytics artifact")
		} else {
			// notify
			m.sendToWebhookNotifier(room.RoomId, room.Sid, "analytics_proceeded", artifact.ArtifactId)
		}
	} else {
		log.Debug("analytics feature was not enabled for this room, file not saved to DB")
	}

	// remove keys a late event wrote after the export deleted them (lock still held)
	if staleKeys, err := m.rs.ScanKeys(fmt.Sprintf(analyticsRoomKey, roomId) + ":*"); err == nil && len(staleKeys) > 0 {
		if err = m.rs.DeleteKeys(staleKeys); err != nil {
			log.WithError(err).Warn("failed to delete late-arriving analytics keys; TTL will clean them")
		}
	}
}

// markStillConnectedUsersAsLeft adds a synthetic USER_LEFT event (room end time) for users with no left event or whose last join is newer than their last left.
func (m *AnalyticsModel) markStillConnectedUsersAsLeft(room *dbmodels.RoomInfo, endedAt time.Time, log *logrus.Entry) {
	endedMs := endedAt.UnixMilli()
	source := "endedAt parameter"
	if !room.Ended.IsZero() {
		endedMs = room.Ended.UnixMilli()
		source = "db recorded room end time"
	}
	log.WithField("source", source).Debugf("using room end time %d for synthetic participant_left events", endedMs)

	k := fmt.Sprintf(analyticsRoomKey+":room:users", room.RoomId)
	users, err := m.rs.AnalyticsGetAllUsers(k)
	if err != nil {
		log.WithError(err).Warn("failed to get analytics users from redis, skipping synthetic participant_left insertion")
		return
	}
	if len(users) == 0 {
		return
	}

	var inserted int
	for userId := range users {
		userKey := fmt.Sprintf(analyticsUserKey, room.RoomId, userId)
		leftKey := fmt.Sprintf("%s:%s", userKey, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_LEFT.String())
		joinKey := fmt.Sprintf("%s:%s", userKey, plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED.String())

		leftTimes, err := m.rs.GetAnalyticsAllHashTypeVals(leftKey)
		if err != nil {
			// treat read errors as no left events
			log.WithError(err).WithField("user_id", userId).Warn("failed to read participant_left events, treating as empty")
			leftTimes = map[string]string{}
		}
		joinTimes, err := m.rs.GetAnalyticsAllHashTypeVals(joinKey)
		if err != nil {
			log.WithError(err).WithField("user_id", userId).Warn("failed to read participant_joined events, treating as empty")
			joinTimes = map[string]string{}
		}

		var maxLeft, maxJoin int64
		for field := range leftTimes {
			if t, perr := strconv.ParseInt(field, 10, 64); perr == nil && t > maxLeft {
				maxLeft = t
			}
		}
		for field := range joinTimes {
			if t, perr := strconv.ParseInt(field, 10, 64); perr == nil && t > maxJoin {
				maxJoin = t
			}
		}

		if len(leftTimes) == 0 || maxJoin > maxLeft {
			val := map[string]string{
				fmt.Sprintf("%d", endedMs): fmt.Sprintf("%d", endedMs),
			}
			if err = m.rs.AddAnalyticsHSETType(leftKey, val); err != nil {
				log.WithError(err).WithField("user_id", userId).Errorln("AddAnalyticsHSETType failed")
				continue
			}
			inserted++
		}
	}

	if inserted > 0 {
		log.Infof("inserted synthetic left event for %d still-connected user(s) at room end time", inserted)
	}
}

func (m *AnalyticsModel) exportAnalyticsToJSON(room *dbmodels.RoomInfo, metadata *plugnmeet.RoomMetadata, log *logrus.Entry) ([]byte, error) {
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
	allKeys, err := m.rs.ScanKeys(scanPattern)
	if err != nil {
		log.WithError(err).Error("failed to scan analytics keys for room")
		return nil, err
	}
	if len(allKeys) == 0 {
		log.Info("No analytics keys found, file will contain only basic room info")
	} else {
		log.Infof("Found %d total analytics keys to process", len(allKeys))
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
	log.Infof("Processed %d room-level analytics keys", roomKeysProcessed)

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

		log.WithField("user_id", i).Infof("Processed %d analytics keys for user", userKeysProcessed)
		usersInfo = append(usersInfo, userInfo)
	}

	var marshal []byte
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
		marshal, err = op.Marshal(result)
		if err != nil {
			log.WithError(err).Error("Failed to marshal analytics result")
			return nil, err
		}
	}

	// at the end delete all redis records
	// also add the users key to the deletion list
	usersKey := fmt.Sprintf(analyticsRoomKey+":room:users", room.RoomId)
	allKeys = append(allKeys, usersKey)

	if err = m.rs.DeleteKeys(allKeys); err != nil {
		log.WithError(err).Error("Failed to delete analytics keys from redis")
	}

	return marshal, err
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
