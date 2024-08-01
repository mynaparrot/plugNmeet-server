package analyticsmodel

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"os"
	"strconv"
	"strings"
	"time"
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

	metadata, err := m.lk.UnmarshalRoomMetadata(meta)
	if err != nil {
		return
	}

	// let's wait a few seconds so that all other processes will finish
	time.Sleep(config.WaitBeforeAnalyticsStartProcessing)

	// we'll check if the room is still active or not.
	// this may happen when we closed the room & re-created it instantly
	exist, err := m.rs.ManageActiveRoomsWithMetadata(roomId, "get", "")
	if err == nil && exist != nil {
		log.Infoln("this room:", roomId, "still active, so we won't process to export analytics")
		return
	}

	if _, err := os.Stat(*m.app.AnalyticsSettings.FilesStorePath); os.IsNotExist(err) {
		err = os.MkdirAll(*m.app.AnalyticsSettings.FilesStorePath, os.ModePerm)
		if err != nil {
			log.Errorln(err)
			return
		}
	}

	isRunning := 0
	room, _ := m.ds.GetRoomInfoBySid(sid, &isRunning)
	if room.ID == 0 {
		return
	}

	fileId := fmt.Sprintf("%s-%d", room.Sid, room.CreationTime)
	path := fmt.Sprintf("%s/%s.json", *config.GetConfig().AnalyticsSettings.FilesStorePath, fileId)

	// export file
	stat, err := m.exportAnalyticsToFile(room, path, metadata)
	if err != nil {
		return
	}

	// it's not possible to get room metadata as always
	// so, if room didn't have activated analytics feature,
	// we will simply won't create the in exportAnalyticsToFile method
	// and won't record to DB
	if metadata.RoomFeatures.EnableAnalytics {
		// record in db
		m.AddAnalyticsFileToDB(room.ID, room.CreationTime, room.RoomId, fileId, stat)
		// notify
		m.sendToWebhookNotifier(room.RoomId, room.Sid, "analytics_proceeded", fileId)
	}
}

func (m *AnalyticsModel) exportAnalyticsToFile(room *dbmodels.RoomInfo, path string, metadata *plugnmeet.RoomMetadata) (os.FileInfo, error) {
	roomInfo := &plugnmeet.AnalyticsRoomInfo{
		RoomId:       room.RoomId,
		RoomTitle:    room.RoomTitle,
		RoomCreation: room.CreationTime,
		RoomEnded:    room.Ended.Unix(),
		EnabledE2Ee:  metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetIsEnabled(),
		Events:       []*plugnmeet.AnalyticsEventData{},
	}
	var usersInfo []*plugnmeet.AnalyticsUserInfo
	var allKeys []string

	key := fmt.Sprintf(analyticsRoomKey+":room", room.RoomId)
	// we can store all users' type key to make things faster
	var userRedisKeys []string
	// we'll collect all room related events
	for _, ev := range plugnmeet.AnalyticsEvents_name {
		if strings.Contains(ev, "ANALYTICS_EVENT_ROOM_") {
			ekey := fmt.Sprintf(key+":%s", ev)
			allKeys = append(allKeys, ekey)

			ev = strings.ToLower(strings.Replace(ev, "ANALYTICS_EVENT_ROOM_", "", 1))
			eventInfo := &plugnmeet.AnalyticsEventData{
				Name:  ev,
				Total: 0,
			}

			err := m.buildEventInfo(ekey, eventInfo)
			if err != nil {
				continue
			}

			roomInfo.Events = append(roomInfo.Events, eventInfo)
		} else {
			// otherwise will be user type
			userRedisKeys = append(userRedisKeys, ev)
		}
	}

	// get users first
	k := fmt.Sprintf("%s:users", key)
	users, err := m.rs.AnalyticsGetAllUsers(k)
	allKeys = append(allKeys, fmt.Sprintf("%s:users", key))
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	roomInfo.RoomTotalUsers = int64(len(users))
	roomInfo.RoomDuration = roomInfo.RoomEnded - roomInfo.RoomCreation

	// now users related events
	for i, n := range users {
		uf := new(plugnmeet.AnalyticsRedisUserInfo)
		_ = protojson.Unmarshal([]byte(n), uf)
		userInfo := &plugnmeet.AnalyticsUserInfo{
			UserId:  i,
			Name:    *uf.Name,
			IsAdmin: uf.IsAdmin,
			Events:  []*plugnmeet.AnalyticsEventData{},
		}

		for _, ev := range userRedisKeys {
			if strings.Contains(ev, "ANALYTICS_EVENT_USER_") {
				ekey := fmt.Sprintf(analyticsUserKey, room.RoomId, i)
				ekey = fmt.Sprintf("%s:%s", ekey, ev)
				allKeys = append(allKeys, ekey)

				ev = strings.ToLower(strings.Replace(ev, "ANALYTICS_EVENT_USER_", "", 1))
				eventInfo := &plugnmeet.AnalyticsEventData{
					Name:  ev,
					Total: 0,
				}
				err = m.buildEventInfo(ekey, eventInfo)
				if err != nil {
					continue
				}
				userInfo.Events = append(userInfo.Events, eventInfo)
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
			log.Errorln(err)
			return nil, err
		}

		err = os.WriteFile(path, marshal, 0644)
		if err != nil {
			log.Errorln(err)
			return nil, err
		}
		stat, err = os.Stat(path)
		if err != nil {
			log.Errorln(err)
			return nil, err
		}

	}

	// at the end delete all redis records
	err = m.rs.AnalyticsDeleteKeys(allKeys)
	if err != nil {
		log.Errorln(err)
	}

	return stat, err
}

func (m *AnalyticsModel) buildEventInfo(ekey string, eventInfo *plugnmeet.AnalyticsEventData) error {
	// we'll check type first
	rType, err := m.rs.AnalyticsGetKeyType(ekey)
	if err != nil {
		log.Println(err)
		return err
	}
	if rType == "hash" {
		var evals []*plugnmeet.AnalyticsEventValue
		result, err := m.rs.GetAnalyticsAllHashTypeVals(ekey)
		if err != nil {
			log.Errorln(err)
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
			log.Println(err)
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
	n := models.GetWebhookNotifier()
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
			log.Errorln(err)
		}
	}
}
