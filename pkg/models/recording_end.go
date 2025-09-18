package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RecordingModel) recordingEnded(r *plugnmeet.RecorderToPlugNmeet) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":      r.RoomId,
		"roomSid":     r.RoomSid,
		"recorderId":  r.RecorderId,
		"roomTableId": r.RoomTableId,
		"method":      "recordingEnded",
	})
	log.Infoln("processing recording_ended event from recorder")

	_, err := m.ds.UpdateRoomRecordingStatus(uint64(r.RoomTableId), 0, nil)
	if err != nil {
		log.WithError(err).Errorln("error updating room recording status in db")
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

	roomMeta.IsRecording = false
	if err = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta); err != nil {
		log.WithError(err).Errorln("failed to update and broadcast room metadata")
	}

	if r.Status {
		err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.recording-ended", false, nil)
	} else {
		err = m.natsService.NotifyErrorMsg(r.RoomId, "notifications.recording-ended-with-error", nil)
	}
	if err != nil {
		log.WithError(err).Errorln("error sending notification message")
	}
	log.Infoln("finished processing recording_ended event")
}
