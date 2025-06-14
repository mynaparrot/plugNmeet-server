package models

import (
	"context"
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

func (m *RoomModel) CreateRoom(ctx context.Context, r *plugnmeet.CreateRoomReq) (*plugnmeet.ActiveRoomInfo, error) {
	// we'll lock the same room creation until the room is created
	lockValue, err := acquireRoomCreationLockWithRetry(ctx, m.rs, r.GetRoomId())
	if err != nil {
		return nil, err // Error already logged by helper
	}

	// Defer unlock using the obtained lockValue for safety.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if unlockErr := m.rs.UnlockRoomCreation(unlockCtx, r.GetRoomId(), lockValue); unlockErr != nil {
			// UnlockRoomCreation in RedisService should log details
			log.Errorf("Error trying to clean up room creation lock for room %s : %v", r.GetRoomId(), unlockErr)
		}
	}()

	// check if room already exists in db or not
	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if err != nil {
		return nil, err
	}

	// handle existing room logic
	if roomDbInfo != nil && roomDbInfo.Sid != "" {
		ari, err := m.handleExistingRoom(r, roomDbInfo)
		if err != nil {
			return nil, err
		}
		if ari != nil {
			return ari, nil
		}
		// otherwise we'll keep going
	}

	// initialize room defaults
	m.setRoomDefaults(r)

	// prepare DB model
	roomDbInfo, sid := m.prepareRoomDbInfo(r, roomDbInfo)

	// save info to db
	_, err = m.ds.InsertOrUpdateRoomInfo(roomDbInfo)
	if err != nil {
		return nil, err
	}

	// now create room bucket
	err = m.natsService.AddRoom(roomDbInfo.ID, r.RoomId, sid, r.EmptyTimeout, r.MaxParticipants, r.Metadata)
	if err != nil {
		return nil, err
	}

	// create streams
	err = m.natsService.CreateRoomNatsStreams(r.RoomId)
	if err != nil {
		return nil, err
	}

	rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil || rInfo == nil {
		return nil, errors.New("room not found in KV")
	}

	// preload whiteboard file if needed
	if !r.Metadata.IsBreakoutRoom {
		go m.prepareWhiteboardPreloadFile(r.Metadata, r.RoomId, sid)
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

	// create and send room_created webhook
	go m.sendRoomCreatedWebhook(ari, r.EmptyTimeout, r.MaxParticipants)

	return ari, nil
}

// handleExistingRoom to handle logic if room already exists
func (m *RoomModel) handleExistingRoom(r *plugnmeet.CreateRoomReq, roomDbInfo *dbmodels.RoomInfo) (*plugnmeet.ActiveRoomInfo, error) {
	rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		return nil, err
	}
	if rInfo != nil && rInfo.DbTableId == roomDbInfo.ID {
		// so, we found the same room
		// in this case we'll just create streams for safety
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
	return nil, nil
}

// setRoomDefaults to Sets default values and metadata
func (m *RoomModel) setRoomDefaults(r *plugnmeet.CreateRoomReq) {
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, m.app.UploadFileSettings.MaxSize, m.app.UploadFileSettings.MaxSizeWhiteboardFile, m.app.UploadFileSettings.AllowedTypes, m.app.SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)
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
		if r.Metadata.CopyrightConf != nil && !copyrightConf.AllowOverride {
			r.Metadata.CopyrightConf = d
		} else if r.Metadata.CopyrightConf == nil {
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
		r.Metadata.RoomFeatures.EnableAnalytics = false
	}
}

// prepareRoomDbInfo Prepares DB model for room
func (m *RoomModel) prepareRoomDbInfo(r *plugnmeet.CreateRoomReq, existing *dbmodels.RoomInfo) (*dbmodels.RoomInfo, string) {
	sId := uuid.New().String()
	isBreakoutRoom := 0
	if r.Metadata.IsBreakoutRoom {
		isBreakoutRoom = 1
	}

	if existing == nil {
		existing = &dbmodels.RoomInfo{
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
		existing.Sid = sId
	}
	if r.Metadata.WebhookUrl != nil {
		existing.WebhookUrl = *r.Metadata.WebhookUrl
	}
	return existing, sId
}

// prepareWhiteboardPreloadFile preload whiteboard file
func (m *RoomModel) prepareWhiteboardPreloadFile(meta *plugnmeet.RoomMetadata, roomId, roomSid string) {
	wbf := meta.RoomFeatures.WhiteboardFeatures
	if wbf == nil || !wbf.AllowedWhiteboard || wbf.PreloadFile == nil || *wbf.PreloadFile == "" {
		return
	}

	log.Infoln(fmt.Sprintf("roomId: %s has preloadFile: %s for whiteboard so, preparing it", roomId, *wbf.PreloadFile))

	fm := NewFileModel(m.app, m.ds, m.natsService)
	err := fm.DownloadAndProcessPreUploadWBfile(roomId, roomSid, *wbf.PreloadFile)
	if err != nil {
		log.Errorln(err)
		_ = m.natsService.NotifyErrorMsg(roomId, "notifications.preloaded-whiteboard-file-processing-error", nil)
		meta.RoomFeatures.WhiteboardFeatures.PreloadFile = nil
		_ = m.natsService.UpdateAndBroadcastRoomMetadata(roomId, meta)
		return
	}

	log.Infoln(fmt.Sprintf("preloadFile: %s for roomId: %s had been processed successfully", *wbf.PreloadFile, roomId))
}

// sendRoomCreatedWebhook to send webhook
func (m *RoomModel) sendRoomCreatedWebhook(info *plugnmeet.ActiveRoomInfo, emptyTimeout, maxParticipants *uint32) {
	n := helpers.GetWebhookNotifier(m.app)
	if n != nil {
		n.RegisterWebhook(info.RoomId, info.Sid)
		e := "room_created"
		cr := uint64(info.CreationTime)
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &e,
			Room: &plugnmeet.NotifyEventRoom{
				RoomId:          &info.RoomId,
				Sid:             &info.Sid,
				CreationTime:    &cr,
				Metadata:        &info.Metadata,
				EmptyTimeout:    emptyTimeout,
				MaxParticipants: maxParticipants,
			},
		}

		err := n.SendWebhookEvent(msg)
		if err != nil {
			log.Errorln(err)
		}
	}
}
