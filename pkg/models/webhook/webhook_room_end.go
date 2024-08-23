package webhookmodel

import (
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/breakoutroom"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/etherpad"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/file"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/poll"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/recorder"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roomduration"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/speechtotext"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *WebhookModel) roomFinished(event *livekit.WebhookEvent) {
	if event.Room == nil {
		log.Errorln("empty roomInfo")
		return
	}

	rInfo, err := m.natsService.GetRoomInfo(event.Room.Name)
	if err != nil {
		log.Errorln(err)
		return
	}

	// update db table
	room := &dbmodels.RoomInfo{
		Sid:       rInfo.RoomId,
		IsRunning: 0,
		Ended:     time.Now().UTC(),
	}
	_, err = m.ds.UpdateRoomStatus(room)
	if err != nil {
		log.Errorln(err)
	}

	event.Room.Metadata = rInfo.Metadata
	event.Room.Sid = rInfo.RoomSid

	go func() {
		// we are introducing a new event name here
		// because for our case we still have remaining tasks
		m.sendCustomTypeWebhook(event, "session_ended")
	}()

	// now we'll perform a few service related tasks
	time.Sleep(config.WaitBeforeTriggerOnAfterRoomEnded)
	m.rm.OnAfterRoomClosed(rInfo.RoomId)

	//we'll send a message to the recorder to stop
	recorderModel := recordermodel.New(m.app, m.ds, m.rs, m.lk)
	err = recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{
		Task:   plugnmeet.RecordingTasks_STOP,
		Sid:    rInfo.RoomSid,
		RoomId: rInfo.RoomId,
	})
	if err != nil {
		log.Errorln(err)
	}

	// few related task can be done in separate goroutine
	go m.onAfterRoomFinishedTasks(rInfo.RoomId, rInfo.RoomSid, rInfo.Metadata)

	// at the end we'll handle event notification
	go func() {
		// send first
		m.sendToWebhookNotifier(event)
		// now clean up
		err := m.webhookNotifier.DeleteWebhook(rInfo.RoomSid)
		if err != nil {
			log.Errorln(err)
		}
	}()
}

func (m *WebhookModel) onAfterRoomFinishedTasks(roomId, roomSid, metadata string) {
	// Delete all the files those may upload during session
	if !m.app.UploadFileSettings.KeepForever {
		fileM := filemodel.New(m.app, m.ds, m.rs, m.lk)
		fileM.AddRequest(&filemodel.FileUploadReq{
			Sid: roomSid,
		})
		err := fileM.DeleteRoomUploadedDir()
		if err != nil {
			log.Errorln(err)
		}
	}

	// clear chatroom from memory
	msg := &redisservice.WebsocketToRedis{
		Type:   "deleteRoom",
		RoomId: roomId,
	}
	marshal, err := json.Marshal(msg)
	if err == nil {
		err := m.rs.PublishToWebsocketChannel(config.UserWebsocketChannel, marshal)
		if err != nil {
			log.Errorln(err)
		}
	}

	// notify to clean room from room duration
	rmDuration := roomdurationmodel.New(m.app, m.rs, m.lk)
	err = rmDuration.DeleteRoomWithDuration(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// clean shared note
	em := etherpadmodel.New(m.app, m.ds, m.rs, m.lk)
	_ = em.CleanAfterRoomEnd(roomId, metadata)

	// clean polls
	pm := pollmodel.New(m.app, m.ds, m.rs, m.lk)
	err = pm.CleanUpPolls(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// remove all breakout rooms
	bm := breakoutroommodel.New(m.app, m.ds, m.rs, m.lk)
	err = bm.PostTaskAfterRoomEndWebhook(roomId, metadata)
	if err != nil {
		log.Errorln(err)
	}

	// speech service clean up
	sm := speechtotextmodel.New(m.app, m.ds, m.rs, m.lk)
	// don't need to worry about room sid changes, because we'll compare both
	err = sm.OnAfterRoomEnded(roomId, roomSid)
	if err != nil {
		log.Errorln(err)
	}

	// now clean up session
	m.natsService.OnAfterSessionEndCleanup(roomId)

	// finally, create the analytics file
	m.analyticsModel.PrepareToExportAnalytics(roomId, roomSid, metadata)
}
