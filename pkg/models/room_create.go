package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func (m *RoomModel) CreateRoom(r *plugnmeet.CreateRoomReq) (*plugnmeet.ActiveRoomInfo, error) {
	log := m.logger.WithFields(logrus.Fields{
		"room_id":       r.GetRoomId(),
		"breakout_room": r.GetMetadata().GetIsBreakoutRoom(),
		"method":        "CreateRoom",
	})
	log.Infoln("create room request")
	// we'll lock the same room creation until the room is created
	lockValue, err := acquireRoomCreationLockWithRetry(m.ctx, m.rs, r.GetRoomId(), log)
	if err != nil {
		return nil, err // Error already logged by helper
	}

	// Defer unlock using the obtained lockValue for safety.
	defer func() {
		unlockCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		if unlockErr := m.rs.UnlockRoomCreation(unlockCtx, r.GetRoomId(), lockValue); unlockErr != nil {
			// UnlockRoomCreation in RedisService should log details
			log.WithError(unlockErr).Error("error trying to clean up room creation lock")
		} else {
			log.Info("room creation lock released")
		}
	}()

	// check if room already exists in db or not
	roomDbInfo, err := m.ds.GetRoomInfoByRoomId(r.RoomId, 1)
	if err != nil {
		log.WithError(err).Error("could not get room info from db")
		return nil, err
	}

	// handle existing room logic
	if roomDbInfo != nil && roomDbInfo.Sid != "" {
		log.Info("found existing active room in db, attempting to handle it")
		ari, err := m.handleExistingRoom(r, roomDbInfo, log)
		if err != nil {
			log.WithError(err).Error("failed to handle existing room")
			return nil, err
		}
		if ari != nil {
			log.Info("successfully handled existing room, returning info")
			return ari, nil
		}
		// otherwise, we'll keep going
		log.Info("existing room record was stale or mismatched, proceeding to create a new session")
	}

	// initialize room defaults
	m.setRoomDefaults(r)

	// prepare DB model
	roomDbInfo, sid := m.prepareRoomDbInfo(r, roomDbInfo)

	// save info to db
	_, err = m.ds.InsertOrUpdateRoomInfo(roomDbInfo)
	if err != nil {
		log.WithError(err).Error("failed to insert or update room in db")
		return nil, err
	}
	log = log.WithFields(logrus.Fields{
		"room_sid":    sid,
		"webhook_url": roomDbInfo.WebhookUrl,
	})
	log.Info("room info saved to db")

	// now create room bucket
	err = m.natsService.AddRoom(roomDbInfo.ID, r.RoomId, sid, r.EmptyTimeout, r.MaxParticipants, r.Metadata)
	if err != nil {
		log.WithError(err).Error("failed to add room to nats")
		return nil, err
	}
	log.Info("room added to nats")

	// create streams
	err = m.natsService.CreateRoomNatsStreams(r.RoomId)
	if err != nil {
		log.WithError(err).Error("failed to create nats streams")
		return nil, err
	}
	log.Info("nats streams created")

	rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil || rInfo == nil {
		return nil, fmt.Errorf("room not found in KV")
	}

	// preload whiteboard file if needed
	if !r.Metadata.IsBreakoutRoom {
		go m.prepareWhiteboardPreloadFile(r.Metadata, r.RoomId, sid, log)
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

	log.Info("successfully created new room")
	return ari, nil
}

// handleExistingRoom to handle logic if room already exists
func (m *RoomModel) handleExistingRoom(r *plugnmeet.CreateRoomReq, roomDbInfo *dbmodels.RoomInfo, log *logrus.Entry) (*plugnmeet.ActiveRoomInfo, error) {
	log = log.WithField("subMethod", "handleExistingRoom")
	log.Info("checking NATS for live room info")

	rInfo, err := m.natsService.GetRoomInfo(r.RoomId)
	if err != nil {
		log.WithError(err).Error("failed to get room info from NATS")
		return nil, err
	}

	if rInfo == nil {
		log.Info("no active room found in NATS, proceeding to create a new session")
		return nil, nil
	}

	if rInfo.DbTableId != roomDbInfo.ID {
		log.WithFields(logrus.Fields{
			"nats_db_id": rInfo.DbTableId,
			"db_id":      roomDbInfo.ID,
		}).Warn("NATS room info does not match DB record, proceeding to create a new session")
		return nil, nil
	}

	// The room is active and matches the DB record.
	log.Info("found matching active room in NATS, ensuring streams are active and returning info")
	if err := m.natsService.CreateRoomNatsStreams(r.RoomId); err != nil {
		log.WithError(err).Error("failed to ensure NATS streams are active")
		return nil, err
	}
	if err := m.natsService.UpdateRoomStatus(r.RoomId, natsservice.RoomStatusActive); err != nil {
		log.WithError(err).Error("failed to update room status to active")
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

// setRoomDefaults to Sets default values and metadata
func (m *RoomModel) setRoomDefaults(r *plugnmeet.CreateRoomReq) {
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, m.app.UploadFileSettings.MaxSize, m.app.UploadFileSettings.MaxSizeWhiteboardFile, m.app.UploadFileSettings.AllowedTypes, m.app.SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)
	utils.SetDefaultRoomSettings(m.app.RoomDefaultSettings, r)

	if r.Metadata.RoomFeatures.InsightsFeatures.IsAllow && (m.app.Insights == nil || !m.app.Insights.Enabled) {
		r.Metadata.RoomFeatures.InsightsFeatures.IsAllow = false
	}

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

	if r.Metadata.IsBreakoutRoom && r.Metadata.RoomFeatures.EnableAnalytics {
		r.Metadata.RoomFeatures.EnableAnalytics = false
	}

	if r.Metadata.RoomFeatures.InsightsFeatures != nil {
		if m.app.Insights == nil {
			r.Metadata.RoomFeatures.InsightsFeatures.IsAllow = false
		} else {
			if r.Metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures != nil {
				maxSelectedTranscriptionTransLangs := 2
				if _, serviceCnf, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeTranscription); err == nil {
					if num, ok := serviceCnf.Options["max_selected_trans_langs"]; ok {
						maxSelectedTranscriptionTransLangs = num.(int)
					}
				}
				r.Metadata.RoomFeatures.InsightsFeatures.TranscriptionFeatures.MaxSelectedTransLangs = int32(maxSelectedTranscriptionTransLangs)
			}

			if r.Metadata.RoomFeatures.InsightsFeatures.ChatTranslationFeatures != nil {
				maxSelectedChatTransLangs := 5
				if _, serviceCnf, err := m.app.Insights.GetProviderAccountForService(insights.ServiceTypeTranslation); err == nil {
					if num, ok := serviceCnf.Options["max_selected_trans_langs"]; ok {
						maxSelectedChatTransLangs = num.(int)
					}
				}
				r.Metadata.RoomFeatures.InsightsFeatures.ChatTranslationFeatures.MaxSelectedTransLangs = int32(maxSelectedChatTransLangs)
			}
		}
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
func (m *RoomModel) prepareWhiteboardPreloadFile(meta *plugnmeet.RoomMetadata, roomId, roomSid string, log *logrus.Entry) {
	wbf := meta.RoomFeatures.WhiteboardFeatures
	if wbf == nil || !wbf.IsAllow || wbf.PreloadFile == nil || *wbf.PreloadFile == "" {
		return
	}
	preloadFile := *wbf.PreloadFile
	log = log.WithFields(logrus.Fields{
		"preload_file_url": preloadFile,
		"subMethod":        "prepareWhiteboardPreloadFile",
	})

	log.Info("preparing preloaded whiteboard file")

	res, err := m.fileModel.DownloadAndProcessPreUploadWBfile(roomId, roomSid, preloadFile, log)
	if err != nil {
		log.WithError(err).Error("failed to download and process preloaded whiteboard file")

		if notifyErr := m.natsService.NotifyErrorMsg(roomId, "notifications.preloaded-whiteboard-file-processing-error", nil); notifyErr != nil {
			log.WithError(notifyErr).Error("failed to send notification for whiteboard processing error")
		}
		return
	}

	meta.RoomFeatures.WhiteboardFeatures.PreloadFile = nil
	// TODO: change current metadata update way and think to broadcast differently
	// TODO: may be use ADD_WHITEBOARD_OFFICE_FILE which will be clean approach
	meta.RoomFeatures.WhiteboardFeatures.WhiteboardFileId = res.FileId
	meta.RoomFeatures.WhiteboardFeatures.FileName = res.FileName
	meta.RoomFeatures.WhiteboardFeatures.FilePath = res.FilePath
	meta.RoomFeatures.WhiteboardFeatures.TotalPages = uint32(res.TotalPages)

	if updateErr := m.natsService.UpdateAndBroadcastRoomMetadata(roomId, meta); updateErr != nil {
		log.WithError(updateErr).Error("failed to update room metadata after whiteboard processing error")
	}

	log.Info("preloaded whiteboard file processed successfully")
}

// sendRoomCreatedWebhook to send webhook
func (m *RoomModel) sendRoomCreatedWebhook(info *plugnmeet.ActiveRoomInfo, emptyTimeout, maxParticipants *uint32) {
	if m.webhookNotifier != nil {
		m.webhookNotifier.RegisterWebhook(info.RoomId, info.Sid)
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

		err := m.webhookNotifier.SendWebhookEvent(msg)
		if err != nil {
			m.logger.WithError(err).Errorln("error sending room created webhook")
		}
	}
}
