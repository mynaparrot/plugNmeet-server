package models

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	analyticsRoomKey = "pnm:analytics:%s"
	analyticsUserKey = analyticsRoomKey + ":user:%s"
)

type AnalyticsModel struct {
	sync.RWMutex
	rc   *redis.Client
	ctx  context.Context
	data *plugnmeet.AnalyticsDataMsg
	rs   *RoomService
}

func NewAnalyticsModel() *AnalyticsModel {
	return &AnalyticsModel{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
		rs:  NewRoomService(),
	}
}

func (m *AnalyticsModel) HandleEvent(d *plugnmeet.AnalyticsDataMsg) {
	if config.AppCnf.AnalyticsSettings == nil ||
		!config.AppCnf.AnalyticsSettings.Enabled {
		return
	}
	m.Lock()
	defer m.Unlock()
	// we'll use unix milliseconds to make sure fields are unique
	d.Time = time.Now().UnixMilli()
	m.data = d

	switch d.EventType {
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM:
		m.handleRoomTypeEvents()
	case plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER:
		m.handleUserTypeEvents()
	}
}

func (m *AnalyticsModel) HandleWebSocketData(dataMsg *plugnmeet.DataMessage) {
	d := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER,
		RoomId:    dataMsg.RoomId,
		UserId:    &dataMsg.Body.From.UserId,
	}
	var val int64 = 1

	switch dataMsg.Body.GetType() {
	case plugnmeet.DataMsgBodyType_CHAT:
		if dataMsg.Body.IsPrivate != nil && *dataMsg.Body.IsPrivate == 1 {
			d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_PRIVATE_CHAT
		} else {
			d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_PUBLIC_CHAT
		}
		d.EventValueInteger = &val
	case plugnmeet.DataMsgBodyType_SCENE_UPDATE:
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_WHITEBOARD_ANNOTATED
		d.EventValueInteger = &val
	case plugnmeet.DataMsgBodyType_USER_VISIBILITY_CHANGE:
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_INTERFACE_VISIBILITY
		d.HsetValue = &dataMsg.Body.Msg
	case plugnmeet.DataMsgBodyType_RAISE_HAND:
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_RAISE_HAND
	}

	m.HandleEvent(d)
}

func (m *AnalyticsModel) handleRoomTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsRoomKey+":room", m.data.RoomId)

	switch m.data.GetEventName() {
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED:
		m.handleFirstTimeUserJoined(key)
		// we still need to run as user type too
		m.handleUserTypeEvents()
	default:
		m.insertEventData(key)
	}
}

func (m *AnalyticsModel) handleUserTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, m.data.RoomId, m.data.GetUserId())
	m.insertEventData(key)
}

func (m *AnalyticsModel) insertEventData(key string) {
	if m.data.EventValueInteger == nil && m.data.EventValueString == nil {
		// so this will be HSET type
		var val map[string]string
		if m.data.HsetValue != nil {
			val = map[string]string{
				fmt.Sprintf("%d", m.data.Time): m.data.GetHsetValue(),
			}
		} else {
			val = map[string]string{
				fmt.Sprintf("%d", m.data.Time): fmt.Sprintf("%d", m.data.Time),
			}
		}
		_, err := m.rc.HSet(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), val).Result()
		if err != nil {
			log.Errorln(err)
		}
	} else if m.data.EventValueInteger != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		_, err := m.rc.IncrBy(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), m.data.GetEventValueInteger()).Result()
		if err != nil {
			log.Errorln(err)
		}
	} else if m.data.EventValueString != nil {
		// we are assuming that we want to set the supplied value
		// in this case we'll simply use SET, which will be string type
		_, err := m.rc.Set(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), m.data.GetEventValueString(), time.Duration(0)).Result()
		if err != nil {
			log.Errorln(err)
		}
	}
}

func (m *AnalyticsModel) PrepareToExportAnalytics(roomId, sid, meta string) {
	if config.AppCnf.AnalyticsSettings == nil || !config.AppCnf.AnalyticsSettings.Enabled {
		return
	}

	// if no metadata then it will be hard to make next logics
	// if still there was some data stored in redis
	// we will have to think different way to clean those
	if meta == "" || sid == "" {
		return
	}

	rms := NewRoomService()
	metadata, err := rms.UnmarshalRoomMetadata(meta)
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

	if _, err := os.Stat(*config.AppCnf.AnalyticsSettings.FilesStorePath); os.IsNotExist(err) {
		err = os.MkdirAll(*config.AppCnf.AnalyticsSettings.FilesStorePath, os.ModePerm)
		if err != nil {
			log.Errorln(err)
			return
		}
	}

	rm := NewRoomModel()
	room, _ := rm.GetRoomInfo("", sid, 0)
	if room.Id == 0 {
		return
	}

	fileId := fmt.Sprintf("%s-%d", room.Sid, room.CreationTime)
	path := fmt.Sprintf("%s/%s.json", *config.AppCnf.AnalyticsSettings.FilesStorePath, fileId)

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
		m.addAnalyticsFileToDB(room.Id, room.CreationTime, room.RoomId, fileId, stat)
		// notify
		m.sendToWebhookNotifier(room.RoomId, room.Sid, "analytics_proceeded", fileId)
	}
}

func (m *AnalyticsModel) exportAnalyticsToFile(room *RoomInfo, path string, metadata *plugnmeet.RoomMetadata) (os.FileInfo, error) {
	ended, _ := time.Parse("2006-01-02 15:04:05", room.Ended)
	roomInfo := &plugnmeet.AnalyticsRoomInfo{
		RoomId:       room.RoomId,
		RoomTitle:    room.RoomTitle,
		RoomCreation: room.CreationTime,
		RoomEnded:    ended.Unix(),
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
	users, err := m.rc.HGetAll(m.ctx, fmt.Sprintf("%s:users", key)).Result()
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
	_, err = m.rc.Del(m.ctx, allKeys...).Result()
	if err != nil {
		log.Errorln(err)
	}

	return stat, err
}

func (m *AnalyticsModel) buildEventInfo(ekey string, eventInfo *plugnmeet.AnalyticsEventData) error {
	// we'll check type first
	rType, err := m.rc.Type(m.ctx, ekey).Result()
	if err != nil {
		log.Println(err)
		return err
	}
	if rType == "hash" {
		var evals []*plugnmeet.AnalyticsEventValue
		result, err := m.rc.HGetAll(m.ctx, ekey).Result()
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
		result, err := m.rc.Get(m.ctx, ekey).Result()
		if err != redis.Nil && err != nil {
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

func (m *AnalyticsModel) addAnalyticsFileToDB(roomTableId, roomCreationTime int64, roomId, fileId string, stat os.FileInfo) {
	db := config.AppCnf.DB
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Errorln(err)
		return
	}
	defer tx.Rollback()

	query := "INSERT INTO " + config.AppCnf.FormatDBTable("room_analytics") + " (room_table_id, room_id, file_id, file_name, file_size, room_creation_time, creation_time) VALUES (?, ?, ?, ?, ?, ?, ?)"

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		log.Errorln(err)
		return
	}

	fsize := float64(stat.Size())
	// we'll convert bytes to KB
	if fsize > 1000 {
		fsize = fsize / 1000.0
	} else {
		fsize = 1
	}

	_, err = stmt.ExecContext(ctx, roomTableId, roomId, fileId, fileId+".json", fsize, roomCreationTime, time.Now().Unix())
	if err != nil {
		log.Errorln(err)
		return
	}

	err = tx.Commit()
	if err != nil {
		log.Errorln(err)
		return
	}

	err = stmt.Close()
	if err != nil {
		log.Errorln(err)
		return
	}
}

func (m *AnalyticsModel) handleFirstTimeUserJoined(key string) {
	umeta := new(plugnmeet.UserMetadata)
	if m.data.ExtraData != nil && *m.data.ExtraData != "" {
		rs := NewRoomService()
		umeta, _ = rs.UnmarshalParticipantMetadata(*m.data.ExtraData)
	}

	uInfo := &plugnmeet.AnalyticsRedisUserInfo{
		Name:    m.data.UserName,
		IsAdmin: umeta.IsAdmin,
	}
	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(uInfo)
	if err != nil {
		log.Errorln(err)
	}

	u := map[string]string{
		*m.data.UserId: string(marshal),
	}
	_, err = m.rc.HSet(m.ctx, fmt.Sprintf("%s:users", key), u).Result()
	if err != nil {
		log.Errorln(err)
	}
}

func (m *AnalyticsModel) sendToWebhookNotifier(roomId, roomSid, task, fileId string) {
	n := GetWebhookNotifier(roomId, roomSid)
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

		err := n.SendWebhook(msg, nil)
		if err != nil {
			log.Errorln(err)
		}
	}
}
