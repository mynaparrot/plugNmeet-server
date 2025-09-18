package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RecordingModel) rtmpStarted(r *plugnmeet.RecorderToPlugNmeet) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"roomSid":     r.RoomSid,
		"recorderId":  r.RecorderId,
		"roomTableId": r.RoomTableId,
		"method":      "rtmpStarted",
	})
	log.Infoln("processing rtmp_started event from recorder")

	_, err := m.ds.UpdateRoomRTMPStatus(uint64(r.RoomTableId), 1, &r.RecorderId)
	if err != nil {
		log.WithError(err).Errorln("error updating room rtmp status in db")
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return
	}
	if roomMeta == nil {
		log.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsActiveRtmp = true
	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.rtmp-started", false, nil)
	if err != nil {
		log.WithError(err).Errorln("error sending notification message")
	}
	log.Infoln("finished processing rtmp_started event")
}

// rtmpEnded will call when the recorder ends rtmp broadcasting
func (m *RecordingModel) rtmpEnded(r *plugnmeet.RecorderToPlugNmeet) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"roomSid":     r.RoomSid,
		"recorderId":  r.RecorderId,
		"roomTableId": r.RoomTableId,
		"method":      "rtmpEnded",
	})
	log.Infoln("processing rtmp_ended event from recorder")

	_, err := m.ds.UpdateRoomRTMPStatus(uint64(r.RoomTableId), 0, nil)
	if err != nil {
		log.WithError(err).Errorln("error updating room rtmp status in db")
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		log.WithError(err).Errorln("failed to get room metadata")
		return
	}
	if roomMeta == nil {
		log.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsActiveRtmp = false
	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	if r.Status {
		err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.rtmp-ended", false, nil)
	} else {
		err = m.natsService.NotifyErrorMsg(r.RoomId, "notifications.rtmp-ended-with-error", nil)
	}
	if err != nil {
		log.WithError(err).Errorln("error sending notification message")
	}
	log.Infoln("finished processing rtmp_ended event")
}
