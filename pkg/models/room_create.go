package models

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	log "github.com/sirupsen/logrus"
	"time"
)

func (m *RoomModel) CreateRoom(r *plugnmeet.CreateRoomReq) (*plugnmeet.ActiveRoomInfo, error) {
	// some pre-creation tasks
	m.preRoomCreationTasks(r)
	// in preRoomCreationTasks we've added this room in progress list
	// so, we'll just use deferring to clean this room at the end of this function
	defer m.rs.UnlockRoomCreation(r.RoomId)

	// check if room already exists in db or not
	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if err != nil {
		return nil, err
	}
	if roomDbInfo != nil && roomDbInfo.Sid != "" {
		// this room was created before
		// so, we'll check our kv
		rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
		if err != nil {
			return nil, err
		}
		if rInfo != nil && rInfo.DbTableId == roomDbInfo.ID {
			// want to make sure our stream was created properly
			err := m.natsService.CreateRoomNatsStreams(r.RoomId)
			if err != nil {
				return nil, err
			}
			err = m.natsService.UpdateRoomStatus(r.RoomId, natsservice.RoomStatusActive)
			if err != nil {
				return nil, err
			}
			return &plugnmeet.ActiveRoomInfo{
				RoomId:       rInfo.RoomId,
				Sid:          rInfo.RoomSid,
				RoomTitle:    roomDbInfo.RoomTitle,
				IsRunning:    1,
				CreationTime: roomDbInfo.CreationTime,
				WebhookUrl:   roomDbInfo.WebhookUrl,
				Metadata:     rInfo.Metadata,
			}, nil
		}
	}
	// otherwise, we're good to continue
	// we can reuse this same db table as no sid had been assigned.

	// we'll set default values otherwise the client got confused if data is missing
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, m.app.UploadFileSettings.MaxSize, m.app.UploadFileSettings.AllowedTypes, m.app.SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)
	// set default room settings
	utils.SetDefaultRoomSettings(m.app.RoomDefaultSettings, r)

	// copyright
	copyrightConf := m.app.Client.CopyrightConf
	if copyrightConf == nil {
		r.Metadata.CopyrightConf = &plugnmeet.CopyrightConf{
			Display: true,
			Text:    "Powered by <a href=\"https://www.plugnmeet.org\" target=\"_blank\">plugNmeet</a>",
		}
	} else {
		d := &plugnmeet.CopyrightConf{
			Display: copyrightConf.Display,
			Text:    copyrightConf.Text,
		}
		// this mean user has set copyright info by API
		if r.Metadata.CopyrightConf != nil {
			// if not allow overriding, then we will simply use default
			if !copyrightConf.AllowOverride {
				r.Metadata.CopyrightConf = d
			}
		} else {
			r.Metadata.CopyrightConf = d
		}
	}

	// Azure cognitive services
	azu := m.app.AzureCognitiveServicesSpeech
	if !azu.Enabled {
		r.Metadata.RoomFeatures.SpeechToTextTranslationFeatures.IsAllow = false
	} else {
		var maxAllow int32 = 2
		if azu.MaxNumTranLangsAllowSelecting > 0 {
			maxAllow = azu.MaxNumTranLangsAllowSelecting
		}
		r.Metadata.RoomFeatures.SpeechToTextTranslationFeatures.MaxNumTranLangsAllowSelecting = maxAllow
	}

	if r.Metadata.IsBreakoutRoom && r.Metadata.RoomFeatures.EnableAnalytics {
		// at present, we'll disable an analytic report for breakout rooms
		r.Metadata.RoomFeatures.EnableAnalytics = false
	}

	isBreakoutRoom := 0
	sId := uuid.New().String()
	if r.Metadata.IsBreakoutRoom {
		isBreakoutRoom = 1
	}

	if roomDbInfo == nil {
		roomDbInfo = &dbmodels.RoomInfo{
			RoomTitle:          r.Metadata.RoomTitle,
			RoomId:             r.RoomId,
			Sid:                sId,
			JoinedParticipants: 0,
			IsRunning:          1,
			WebhookUrl:         "",
			IsBreakoutRoom:     isBreakoutRoom,
			ParentRoomID:       r.Metadata.ParentRoomId,
		}
	} else {
		// we found the room, we'll just update few info
		roomDbInfo.Sid = sId
	}
	if r.Metadata.WebhookUrl != nil {
		roomDbInfo.WebhookUrl = *r.Metadata.WebhookUrl
	}

	// save info to db
	_, err = m.ds.InsertOrUpdateRoomInfo(roomDbInfo)
	if err != nil {
		return nil, err
	}

	// now create room bucket
	err = m.natsService.AddRoom(roomDbInfo.ID, r.RoomId, sId, r.EmptyTimeout, r.MaxParticipants, r.Metadata)
	if err != nil {
		return nil, err
	}

	if !r.Metadata.IsBreakoutRoom {
		go m.prepareWhiteboardPreloadFile(r.Metadata.RoomFeatures.WhiteboardFeatures, r.RoomId, sId)
	}

	// create streams
	err = m.natsService.CreateRoomNatsStreams(r.RoomId)
	if err != nil {
		return nil, err
	}

	rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		return nil, err
	}
	if rInfo == nil {
		return nil, errors.New("room not found in KV")
	}

	ari := &plugnmeet.ActiveRoomInfo{
		RoomId:       rInfo.RoomId,
		Sid:          rInfo.RoomSid,
		RoomTitle:    roomDbInfo.RoomTitle,
		IsRunning:    1,
		CreationTime: roomDbInfo.CreationTime,
		WebhookUrl:   roomDbInfo.WebhookUrl,
		Metadata:     rInfo.Metadata,
	}

	// create & send room_created webhook
	go m.sendRoomCreatedWebhook(ari)

	return ari, nil
}

func (m *RoomModel) preRoomCreationTasks(r *plugnmeet.CreateRoomReq) {
	// check & wait
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	// we'll lock this room to be created again before this process ended
	// set maximum 1 minute as TTL
	// this way we can ensure that there will not be any deadlock
	// otherwise in various reason key may stay in kv & create deadlock
	err := m.rs.LockRoomCreation(r.GetRoomId(), time.Minute*1)
	if err != nil {
		log.Errorln(err)
	}
}

func (m *RoomModel) prepareWhiteboardPreloadFile(wbf *plugnmeet.WhiteboardFeatures, roomId, roomSid string) {
	if wbf == nil || !wbf.AllowedWhiteboard || wbf.PreloadFile == nil || *wbf.PreloadFile == "" {
		return
	}

	log.Infoln(fmt.Sprintf("roomId: %s has preloadFile: %s for whiteboard so, preparing it", roomId, *wbf.PreloadFile))

	fm := NewFileModel(m.app, m.ds, m.natsService)
	err := fm.DownloadAndProcessPreUploadWBfile(roomId, roomSid, *wbf.PreloadFile)
	if err != nil {
		log.Errorln(err)
		_ = m.natsService.NotifyErrorMsg(roomId, "notifications.preloaded-whiteboard-file-processing-error", nil)
		return
	}

	log.Infoln(fmt.Sprintf("preloadFile: %s for roomId: %s had been processed successfully", *wbf.PreloadFile, roomId))
}

func (m *RoomModel) sendRoomCreatedWebhook(info *plugnmeet.ActiveRoomInfo) {
	n := helpers.GetWebhookNotifier(m.app)
	if n != nil {
		// register for event first
		n.RegisterWebhook(info.RoomId, info.Sid)

		// now send first event
		e := "room_created"
		cr := uint64(info.CreationTime)
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &e,
			Room: &plugnmeet.NotifyEventRoom{
				RoomId:       &info.RoomId,
				Sid:          &info.Sid,
				CreationTime: &cr,
				Metadata:     &info.Metadata,
			},
		}

		err := n.SendWebhookEvent(msg)
		if err != nil {
			log.Errorln(err)
		}
	}
}
