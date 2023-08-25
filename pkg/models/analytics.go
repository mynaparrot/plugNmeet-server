package models

import (
	"context"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"time"
)

const (
	analyticsRoomKey = "pnm:analytics:%s"
	analyticsUserKey = analyticsRoomKey + ":user:%s"
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
	case plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_USER_JOIN:
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
		_, err := m.rc.ZAdd(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), redis.Z{Score: float64(*m.data.Time), Member: *m.data.Time}).Result()
		if err != nil {
			log.Errorln(err)
		}

	}
}

func (m *AnalyticsModel) handleUserTypeEvents() {
	if m.data.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN {
		return
	}
	key := fmt.Sprintf(analyticsUserKey, *m.data.RoomId, *m.data.UserId)
	//fmt.Println(fmt.Sprintf("%s:%s", key, m.data.EventName.String()))

	_, err := m.rc.ZAdd(m.ctx, fmt.Sprintf("%s:%s", key, m.data.EventName.String()), redis.Z{Score: float64(*m.data.Time), Member: *m.data.Time}).Result()
	if err != nil {
		log.Errorln(err)
	}
}

func (m *AnalyticsModel) ExportAnalyticsToFile() {
	//for _, n := range AnalyticsEvents_name {
	//	fmt.Println(n)
	//}
}
