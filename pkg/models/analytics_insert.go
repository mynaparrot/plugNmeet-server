package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *AnalyticsModel) handleFirstTimeUserJoined(d *plugnmeet.AnalyticsDataMsg, key string) {
	umeta := new(plugnmeet.UserMetadata)
	// Use getter for safety and log potential errors.
	if d.GetExtraData() != "" {
		var err error
		umeta, err = m.natsService.UnmarshalUserMetadata(d.GetExtraData())
		if err != nil {
			m.logger.WithError(err).Warn("failed to unmarshal user metadata for analytics")
		}
	}

	uInfo := &plugnmeet.AnalyticsRedisUserInfo{
		Name:     d.UserName,
		IsAdmin:  umeta.IsAdmin,
		ExUserId: umeta.ExUserId,
	}

	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(uInfo)
	if err != nil {
		m.logger.WithError(err).Errorln("marshalling failed")
		return // Don't proceed if marshalling fails.
	}

	u := map[string]string{
		*d.UserId: string(marshal),
	}
	k := fmt.Sprintf("%s:users", key)

	err = m.rs.AddAnalyticsUser(k, u)
	if err != nil {
		m.logger.WithError(err).Errorln("AddAnalyticsUser failed")
	}

	// Also, handle the user_joined event insertion here to avoid a second call.
	userEventKey := fmt.Sprintf(analyticsUserKey, d.RoomId, d.GetUserId())
	m.insertEventData(d, userEventKey)
}

func (m *AnalyticsModel) insertEventData(d *plugnmeet.AnalyticsDataMsg, key string) {
	if d.EventValueInteger == nil && d.EventValueString == nil {
		// so this will be HSET type
		var val map[string]string
		if d.HsetValue != nil {
			val = map[string]string{
				fmt.Sprintf("%d", d.Time): d.GetHsetValue(),
			}
		} else {
			val = map[string]string{
				fmt.Sprintf("%d", d.Time): fmt.Sprintf("%d", d.Time),
			}
		}
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.AddAnalyticsHSETType(k, val)
		if err != nil {
			m.logger.WithError(err).Errorln("AddAnalyticsHSETType failed")
		}

	} else if d.EventValueInteger != nil {
		// we are assuming that the value will be always integer
		// in this case we'll simply use INCRBY, which will be string type
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.IncrementAnalyticsVal(k, d.GetEventValueInteger())
		if err != nil {
			m.logger.WithError(err).Errorln("IncrementAnalyticsVal failed")
		}
	} else if d.EventValueString != nil {
		// we are assuming that we want to set the supplied value
		// in this case we'll simply use SET, which will be string type
		k := fmt.Sprintf("%s:%s", key, d.EventName.String())
		err := m.rs.AddAnalyticsStringType(k, d.GetEventValueString())
		if err != nil {
			m.logger.WithError(err).Errorln("AddAnalyticsStringType failed")
		}
	}
}
