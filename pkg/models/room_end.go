package models

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *RoomModel) EndRoom(r *plugnmeet.RoomEndReq) (bool, string) {
	// check first
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.GetRoomId(), 1)
	if err != nil {
		return false, err.Error()
	}

	if roomDbInfo == nil || roomDbInfo.ID == 0 {
		return false, "room not active"
	}

	info, err := m.natsService.GetRoomInfo(r.GetRoomId())
	if err != nil {
		return false, err.Error()
	}
	if info == nil {
		return false, "room not active"
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SESSION_ENDED, r.GetRoomId(), "notifications.room-disconnected-room-ended", nil)
	if err != nil {
		log.Errorln(err)
	}

	// change room status to delete
	if info.Status != natsservice.RoomStatusEnded {
		err = m.natsService.UpdateRoomStatus(r.GetRoomId(), natsservice.RoomStatusEnded)
		if err != nil {
			log.Errorln(err)
		}

		_, err = m.lk.EndRoom(r.GetRoomId())
		if err != nil {
			log.Errorln(err)
		}
	}

	// process further
	go m.OnAfterRoomEnded(info.RoomId, info.RoomSid, info.Metadata)

	return true, "success"
}

func (m *RoomModel) OnAfterRoomEnded(roomId, roomSid, metadata string) {
	// lock room creation otherwise may have an unexpected result
	// if recreated before clean up completed
	err := m.rs.LockRoomCreation(roomId, config.WaitBeforeTriggerOnAfterRoomEnded+(time.Second*5))
	if err != nil {
		log.Errorln(err)
	}

	// update db status
	_, err = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
		RoomId:    roomId,
		IsRunning: 0,
	})
	if err != nil {
		log.Errorln(err)
	}

	// now we'll perform a few service related tasks
	// if we do not wait, then the result will be wrong
	// because few of the services depend on all user disconnections
	time.Sleep(config.WaitBeforeTriggerOnAfterRoomEnded)

	// delete blocked users list
	m.natsService.DeleteRoomUsersBlockList(roomId)

	//we'll send a message to the recorder to stop
	recorderModel := NewRecorderModel(m.app, m.ds, m.rs)
	err = recorderModel.SendMsgToRecorder(&plugnmeet.RecordingReq{
		Task:   plugnmeet.RecordingTasks_STOP,
		Sid:    roomSid,
		RoomId: roomId,
	})
	if err != nil {
		log.Errorln(err)
	}

	// Delete all the files those may upload during session
	if !m.app.UploadFileSettings.KeepForever {
		fileM := NewFileModel(m.app, m.ds, m.rs)
		err = fileM.DeleteRoomUploadedDir(roomSid)
		if err != nil {
			log.Errorln(err)
		}
	}

	// notify to clean room from room duration
	rmDuration := NewRoomDurationModel(m.app, m.rs)
	err = rmDuration.DeleteRoomWithDuration(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// clean shared note
	em := NewEtherpadModel(m.app, m.ds, m.rs)
	_ = em.CleanAfterRoomEnd(roomId, metadata)

	// clean polls
	pm := NewPollModel(m.app, m.ds, m.rs)
	err = pm.CleanUpPolls(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// remove all breakout rooms
	bm := NewBreakoutRoomModel(m.app, m.ds, m.rs)
	err = bm.PostTaskAfterRoomEndWebhook(roomId, metadata)
	if err != nil {
		log.Errorln(err)
	}

	// speech service clean up
	sm := NewSpeechToTextModel(m.app, m.ds, m.rs)
	// don't need to worry about room sid changes, because we'll compare both
	err = sm.OnAfterRoomEnded(roomId, roomSid)
	if err != nil {
		log.Errorln(err)
	}

	// now clean up session
	m.natsService.OnAfterSessionEndCleanup(roomId)

	log.Infoln(fmt.Sprintf("roomId: %s has been cleaned properly", roomId))
	// release the room
	m.rs.UnlockRoomCreation(roomId)

	// finally, create the analytics file
	analyticsModel := NewAnalyticsModel(m.app, m.ds, m.rs)
	analyticsModel.PrepareToExportAnalytics(roomId, roomSid, metadata)
}
