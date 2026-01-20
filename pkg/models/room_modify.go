package models

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RoomModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return false, err.Error()
	}
	if roomMeta == nil {
		return false, "invalid nil room metadata information"
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}

func (m *RoomModel) EnableRoomSipDialIn(r *plugnmeet.EnableSipDialInReq) error {
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return err
	}

	if roomMeta == nil {
		return fmt.Errorf("invalid nil room metadata information")
	}

	sipDialInFeatures := roomMeta.RoomFeatures.SipDialInFeatures
	if !sipDialInFeatures.IsAllow {
		return fmt.Errorf("sip dial feature isn't allow for this room")
	}

	if sipDialInFeatures.IsActive {
		return fmt.Errorf("sip dial in is already enabled")
	}
	log := m.logger.WithFields(logrus.Fields{
		"room_id": r.RoomId,
		"method":  "EnableRoomSipDialIn",
	})

	ruleId, pin, err := m.lk.CreateSIPDispatchRule(r.RoomId, sipDialInFeatures.HidePhoneNumber, log)
	if err != nil {
		log.WithError(err).Errorln("failed to create SIP dispatch rule")
		return fmt.Errorf("failed to create SIP dispatch rule")
	}
	sipDialInFeatures.IsActive = true
	sipDialInFeatures.DispatchRuleId = &ruleId
	sipDialInFeatures.Pin = &pin
	sipDialInFeatures.PhoneNumbers = m.app.LivekitSipInfo.PhoneNumbers

	return m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)
}
