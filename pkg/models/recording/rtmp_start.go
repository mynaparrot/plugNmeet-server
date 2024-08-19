package recordingmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/datamsg"
	log "github.com/sirupsen/logrus"
)

func (m *RecordingModel) rtmpStarted(r *plugnmeet.RecorderToPlugNmeet) {
	_, err := m.ds.UpdateRoomRTMPStatus(uint64(r.RoomTableId), 1, &r.RecorderId)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	roomMeta, err := m.natsService.GetRoomMetadataStruct(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsActiveRtmp = true
	_ = m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, roomMeta)

	// send message to room
	dm := datamsgmodel.New(m.app, m.ds, m.rs, m.lk)
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: plugnmeet.DataMsgBodyType_INFO,
		Msg:         "notifications.rtmp-started",
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}
