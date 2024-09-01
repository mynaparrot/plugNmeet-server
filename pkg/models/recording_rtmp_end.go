package models

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

// rtmpEnded will call when recorder will end recording
func (m *RecordingModel) rtmpEnded(r *plugnmeet.RecorderToPlugNmeet) {
	_, err := m.ds.UpdateRoomRTMPStatus(uint64(r.RoomTableId), 0, nil)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return
	}
	if roomMeta == nil {
		log.Errorln("invalid nil room metadata information")
		return
	}

	roomMeta.IsActiveRtmp = false
	_ = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	if r.Status {
		err = m.natsService.NotifyInfoMsg(r.RoomId, "notifications.rtmp-ended", false, nil)
	} else {
		err = m.natsService.NotifyErrorMsg(r.RoomId, "notifications.rtmp-ended-with-error", nil)
	}
	if err != nil {
		log.Errorln(err)
	}
}
