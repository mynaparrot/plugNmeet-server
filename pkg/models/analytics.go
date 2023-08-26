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
	"time"
)

const (
	analyticsRoomKey          = "pnm:analytics:%s"
	analyticsUserKey          = analyticsRoomKey + ":user:%s"
	waitBeforeProcessDuration = time.Second * 10
)

type AnalyticsModel struct {
	rc   *redis.Client
	ctx  context.Context
	data *plugnmeet.AnalyticsDataMsg
}

func NewAnalyticsModel() *AnalyticsModel {
	return &AnalyticsModel{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
	}
}

func (m *AnalyticsModel) HandleEvent(d *plugnmeet.AnalyticsDataMsg) {
	if config.AppCnf.AnalyticsSettings == nil ||
		!config.AppCnf.AnalyticsSettings.Enabled {
		return
	}

	now := time.Now().Unix()
	d.Time = &now
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
		RoomId:    &dataMsg.RoomId,
		UserId:    &dataMsg.Body.From.UserId,
	}
	switch dataMsg.Body.Type {
	case plugnmeet.DataMsgBodyType_CHAT:
		if dataMsg.Body.IsPrivate != nil && *dataMsg.Body.IsPrivate == 1 {
			d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_PRIVATE_CHAT
		} else {
			d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_PUBLIC_CHAT
		}
	case plugnmeet.DataMsgBodyType_SCENE_UPDATE:
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_WHITEBOARD_ANNOTATED
	//case plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_FILE,
	//	plugnmeet.DataMsgBodyType_ADD_WHITEBOARD_OFFICE_FILE:
	//	d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_WHITEBOARD_FILES_ADDED
	case plugnmeet.DataMsgBodyType_USER_VISIBILITY_CHANGE:
		if dataMsg.Body.Msg == "hidden" {
			d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_INVISIBLE_INTERFACE
		}
	case plugnmeet.DataMsgBodyType_RAISE_HAND:
		d.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_RAISE_HAND
	}

	m.HandleEvent(d)
}

func (m *AnalyticsModel) handleRoomTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsRoomKey+":room", *m.data.RoomId)

	switch m.data.EventName {
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_USER_JOINED:
		u := map[string]string{
			*m.data.UserId: *m.data.UserName,
		}
		_, err := m.rc.HMSet(m.ctx, fmt.Sprintf("%s:users", key), u).Result()
		if err != nil {
			log.Errorln(err)
		}
		// we still need to run as user type too
		m.handleUserTypeEvents()
	default:
		if m.data.EventValueInteger == nil && m.data.EventValueString == nil {
			_, err := m.rc.ZAdd(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), redis.Z{Score: float64(*m.data.Time), Member: *m.data.Time}).Result()
			if err != nil {
				log.Errorln(err)
			}
		} else if m.data.EventValueInteger != nil {
			// in this case we'll simply use INCRBY, which will be string type
			_, err := m.rc.IncrBy(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), *m.data.EventValueInteger).Result()
			if err != nil {
				log.Errorln(err)
			}
		} else if m.data.EventValueString != nil {
			// if we've event value string then we'll simply set the value
			_, err := m.rc.Set(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), *m.data.EventValueString, time.Duration(0)).Result()
			if err != nil {
				log.Errorln(err)
			}
		}
	}
}

func (m *AnalyticsModel) handleUserTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, *m.data.RoomId, *m.data.UserId)

	if m.data.EventValueInteger == nil {
		_, err := m.rc.ZAdd(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), redis.Z{Score: float64(*m.data.Time), Member: *m.data.Time}).Result()
		if err != nil {
			log.Errorln(err)
		}
	} else if m.data.EventValueInteger != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		_, err := m.rc.IncrBy(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), *m.data.EventValueInteger).Result()
		if err != nil {
			log.Errorln(err)
		}
	} else if m.data.EventValueString != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		_, err := m.rc.Set(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), *m.data.EventValueString, time.Duration(0)).Result()
		if err != nil {
			log.Errorln(err)
		}
	}
}

func (m *AnalyticsModel) PrepareToExportAnalytics(sid, meta string) {
	if config.AppCnf.AnalyticsSettings == nil || !config.AppCnf.AnalyticsSettings.Enabled {
		return
	}

	rms := NewRoomService()
	metadata, err := rms.UnmarshalRoomMetadata(meta)
	if err != nil {
		return
	}

	// let's wait 10 seconds so that all other process will finish
	time.Sleep(waitBeforeProcessDuration)

	if _, err := os.Stat(*config.AppCnf.AnalyticsSettings.FilesStorePath); os.IsNotExist(err) {
		err = os.MkdirAll(*config.AppCnf.AnalyticsSettings.FilesStorePath, os.ModePerm)
		if err != nil {
			log.Errorln(err)
			return
		}
	}

	rm := NewRoomModel()
	room, _ := rm.GetRoomInfo("", sid, 0)
	if room == nil {
		return
	}

	fileId := fmt.Sprintf("%s-%d", room.Sid, room.CreationTime)
	path := fmt.Sprintf("%s/%s.json", *config.AppCnf.AnalyticsSettings.FilesStorePath, fileId)

	// export file
	err = m.exportAnalyticsToFile(room, path, metadata)
	if err != nil {
		return
	}

	// it's not possible to get room metadata as always
	// so, if room didn't have activated analytics feature,
	// we will simply won't create the in exportAnalyticsToFile method
	// and won't record to DB
	if metadata.RoomFeatures.EnableAnalytics {
		// record in db
		m.addAnalyticsFileToDB(room.Id, room.CreationTime, room.RoomId, fileId)
	}
}

func (m *AnalyticsModel) exportAnalyticsToFile(room *RoomInfo, path string, metadata *plugnmeet.RoomMetadata) error {
	ended, _ := time.Parse("2006-01-02 15:04:05", room.Ended)
	roomInfo := &plugnmeet.AnalyticsRoomInfo{
		RoomId:       room.RoomId,
		RoomTitle:    room.RoomTitle,
		RoomCreation: room.CreationTime,
		RoomEnded:    ended.Unix(),
		Events:       []*plugnmeet.AnalyticsEventData{},
	}
	usersInfo := []*plugnmeet.AnalyticsUserInfo{}
	allKeys := []string{}

	key := fmt.Sprintf(analyticsRoomKey+":room", room.RoomId)
	userRedisKeys := []string{}
	// we'll collect all room related events
	for _, ev := range plugnmeet.AnalyticsEvents_name {
		if strings.Contains(ev, "ANALYTICS_EVENT_ROOM_") {
			ekey := fmt.Sprintf(key+":%s", ev)
			allKeys = append(allKeys, ekey)

			ev = strings.ToLower(strings.Replace(ev, "ANALYTICS_EVENT_ROOM_", "", 1))
			eventInfo := &plugnmeet.AnalyticsEventData{
				Name:   ev,
				Total:  0,
				Values: []string{},
			}

			// we'll check type first
			rType, err := m.rc.Type(m.ctx, ekey).Result()
			if err != nil {
				log.Println(err)
				continue
			}

			if rType == "zset" {
				result, err := m.rc.ZRange(m.ctx, ekey, 0, -1).Result()
				if err != nil {
					log.Println(err)
					continue
				}
				eventInfo.Total = uint32(len(result))
				eventInfo.Values = result
			} else {
				result, err := m.rc.Get(m.ctx, ekey).Result()
				if err != redis.Nil && err != nil {
					log.Println(err)
					continue
				}
				if result != "" {
					c, _ := strconv.Atoi(result)
					eventInfo.Total = uint32(c)
				}
			}

			roomInfo.Events = append(roomInfo.Events, eventInfo)
		} else {
			userRedisKeys = append(userRedisKeys, ev)
		}
	}

	// get users first
	users, err := m.rc.HGetAll(m.ctx, fmt.Sprintf("%s:users", key)).Result()
	allKeys = append(allKeys, fmt.Sprintf("%s:users", key))
	if err != nil {
		log.Errorln(err)
		return err
	}
	roomInfo.RoomTotalUsers = int64(len(users))
	roomInfo.RoomDuration = roomInfo.RoomEnded - roomInfo.RoomCreation

	// now users related events
	for i, n := range users {
		userInfo := &plugnmeet.AnalyticsUserInfo{
			UserId: i,
			Name:   n,
			Events: []*plugnmeet.AnalyticsEventData{},
		}

		for _, ev := range userRedisKeys {
			if strings.Contains(ev, "ANALYTICS_EVENT_USER_") {
				ekey := fmt.Sprintf(analyticsUserKey, room.RoomId, i)
				ekey = fmt.Sprintf("%s:%s", ekey, ev)
				allKeys = append(allKeys, ekey)

				ev = strings.ToLower(strings.Replace(ev, "ANALYTICS_EVENT_USER_", "", 1))
				eventInfo := &plugnmeet.AnalyticsEventData{
					Name:   ev,
					Total:  0,
					Values: []string{},
				}

				// we'll check type first
				rType, err := m.rc.Type(m.ctx, ekey).Result()
				if err != nil {
					log.Println(err)
					continue
				}
				if rType == "zset" {
					result, err := m.rc.ZRange(m.ctx, ekey, 0, -1).Result()
					if err != nil {
						log.Errorln(err)
						continue
					}
					eventInfo.Total = uint32(len(result))
					eventInfo.Values = result
				} else {
					result, err := m.rc.Get(m.ctx, ekey).Result()
					if err != redis.Nil && err != nil {
						log.Println(err)
						continue
					}
					if result != "" {
						c, _ := strconv.Atoi(result)
						eventInfo.Total = uint32(c)
					}
				}

				userInfo.Events = append(userInfo.Events, eventInfo)
			}
		}

		usersInfo = append(usersInfo, userInfo)
	}

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
			return err
		}

		err = os.WriteFile(path, marshal, 0644)
		if err != nil {
			log.Errorln(err)
			return err
		}
	}

	// at the end delete all redis records
	_, err = m.rc.Del(m.ctx, allKeys...).Result()
	if err != nil {
		log.Errorln(err)
	}

	return err
}

func (m *AnalyticsModel) addAnalyticsFileToDB(roomTableId, roomCreationTime int64, roomId, fileId string) {
	db := config.AppCnf.DB
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Errorln(err)
		return
	}
	defer tx.Rollback()

	query := "INSERT INTO " + config.AppCnf.FormatDBTable("room_analytics") + " (room_table_id, room_id, file_id, file_name, room_creation_time, creation_time) VALUES (?, ?, ?, ?, ?, ?)"

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		log.Errorln(err)
		return
	}

	_, err = stmt.ExecContext(ctx, roomTableId, roomId, fileId, fileId+".json", roomCreationTime, time.Now().Unix())
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
