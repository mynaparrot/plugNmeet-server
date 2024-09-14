package models

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *AnalyticsModel) handleFirstTimeUserJoined(key string) {
	umeta := new(plugnmeet.UserMetadata)
	if m.data.ExtraData != nil && *m.data.ExtraData != "" {
		umeta, _ = m.natsService.UnmarshalUserMetadata(*m.data.ExtraData)
	}

	uInfo := &plugnmeet.AnalyticsRedisUserInfo{
		Name:     m.data.UserName,
		IsAdmin:  umeta.IsAdmin,
		ExUserId: umeta.ExUserId,
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
	k := fmt.Sprintf("%s:users", key)

	err = m.rs.AddAnalyticsUser(k, u)
	if err != nil {
		log.Errorln(err)
	}
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
		k := fmt.Sprintf("%s:%s", key, m.data.EventName.String())
		err := m.rs.AddAnalyticsHSETType(k, val)
		if err != nil {
			log.Errorln(err)
		}

	} else if m.data.EventValueInteger != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		k := fmt.Sprintf("%s:%s", key, m.data.EventName.String())
		err := m.rs.IncrementAnalyticsVal(k, m.data.GetEventValueInteger())
		if err != nil {
			log.Errorln(err)
		}
	} else if m.data.EventValueString != nil {
		// we are assuming that we want to set the supplied value
		// in this case we'll simply use SET, which will be string type
		k := fmt.Sprintf("%s:%s", key, m.data.EventName.String())
		err := m.rs.AddAnalyticsStringType(k, m.data.GetEventValueString())
		if err != nil {
			log.Errorln(err)
		}
	}
}
