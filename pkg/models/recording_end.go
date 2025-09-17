package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *RecordingModel) recordingEnded(r *plugnmeet.RecorderToPlugNmeet) {
	_, err := m.ds.UpdateRoomRecordingStatus(uint64(r.RoomTableId), 0, nil)
	if err != nil {
		m.logger.WithError(err).Infoln("error updating room recording status")
	}
	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return
	}
	if roomMeta == nil {
		m.logger.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsRecording = false
	_ = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	if r.Status {
		err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.recording-ended", false, nil)
	} else {
		err = m.natsService.NotifyErrorMsg(r.RoomId, "notifications.recording-ended-with-error", nil)
	}
	if err != nil {
		m.logger.WithError(err).Errorln("error sending notification message")
	}
}
