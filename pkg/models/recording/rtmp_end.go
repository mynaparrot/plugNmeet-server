package recordingmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/datamsg"
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

	roomMeta.IsActiveRtmp = false
	_ = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	msg := "notifications.rtmp-ended"
	msgType := plugnmeet.DataMsgBodyType_INFO
	if !r.Status {
		msgType = plugnmeet.DataMsgBodyType_ALERT
		msg = "notifications.rtmp-ended-with-error"
	}
	// send message to room
	dm := datamsgmodel.New(m.app, m.ds, m.rs, m.lk)
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: msgType,
		Msg:         msg,
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}
