package roommodel

import (
	"fmt"
	"github.com/cavaliergopher/grab/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/filemodel"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"time"
)

func (m *RoomModel) CreateRoom(r *plugnmeet.CreateRoomReq) (bool, string, *livekit.Room) {
	// some pre-creation tasks
	m.preRoomCreationTasks(r)

	// in preRoomCreationTasks we've added this room in progress list
	// so, we'll just use deferring to clean this room at the end of this function
	defer m.rs.RoomCreationProgressList(r.RoomId, "del")

	var err error
	roomDbInfo := new(dbmodels.RoomInfo)

	if roomDbInfo, err = m.ds.GetRoomInfoByRoomId(r.RoomId, 1); err == nil &&
		roomDbInfo != nil && roomDbInfo.ID > 0 {
		rf, err := m.lk.LoadRoomInfo(r.RoomId)
		if err != nil && err.Error() != config.RequestedRoomNotExist {
			return false, "can't create room. try again", nil
		}

		if rf != nil && rf.Sid != "" {
			if roomDbInfo.Sid == "" {
				roomDbInfo.Sid = rf.Sid
				// we can just update
				_, err = m.ds.InsertOrUpdateRoomInfo(roomDbInfo)
				if err != nil {
					return false, err.Error(), nil
				}
				return true, "room already exists", rf
			} else if rf.Sid == roomDbInfo.Sid {
				return true, "room already exists", rf
			}
		}

		// We'll allow creating room again & use the same DB row
		// we can just update the DB row.
		// No need to create a new one
	}

	// we'll set default values otherwise the client got confused if data is missing
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, config.GetConfig().UploadFileSettings.MaxSize, config.GetConfig().UploadFileSettings.AllowedTypes, config.GetConfig().SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)
	// set default room settings
	utils.SetDefaultRoomSettings(config.GetConfig().RoomDefaultSettings, r)

	// copyright
	copyrightConf := config.GetConfig().Client.CopyrightConf
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
	azu := config.GetConfig().AzureCognitiveServicesSpeech
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

	meta, err := m.lk.MarshalRoomMetadata(r.Metadata)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	room, err := m.lk.CreateRoom(r.RoomId, r.EmptyTimeout, r.MaxParticipants, meta)
	if err != nil {
		log.Errorln(fmt.Sprintf("room creation error in livekit for %s with error: %s", r.RoomId, err.Error()))
		return false, "Error: " + err.Error(), nil
	}

	if room.Sid == "" {
		log.Errorln(fmt.Sprintf("got empty SID for %s", r.RoomId))
		// without SID, it is hard to manage, if empty then we won't continue
		// in this case we'll end the room to clean up
		_, err = m.lk.EndRoom(r.RoomId)
		if err != nil {
			log.Errorln(err)
		}
		return false, "Error: can't create room with empty SID", nil
	}

	isBreakoutRoom := 0
	if r.Metadata.IsBreakoutRoom {
		isBreakoutRoom = 1
	} else {
		// at present, we'll fetch file for the main room only
		go m.prepareWhiteboardPreloadFile(r, room)
	}

	updateTable := false
	ri := &dbmodels.RoomInfo{
		RoomTitle:          r.Metadata.RoomTitle,
		RoomId:             room.Name,
		Sid:                room.Sid,
		JoinedParticipants: 0,
		IsRunning:          1,
		CreationTime:       room.CreationTime,
		WebhookUrl:         "",
		IsBreakoutRoom:     isBreakoutRoom,
		ParentRoomID:       r.Metadata.ParentRoomId,
	}
	if r.Metadata.WebhookUrl != nil {
		ri.WebhookUrl = *r.Metadata.WebhookUrl
	}

	if roomDbInfo != nil && roomDbInfo.ID > 0 {
		log.Infoln(fmt.Sprintf("running room found for %s, tableId: %d, tableSid: %s, new sid: %s, not creating new db record again", r.RoomId, roomDbInfo.ID, roomDbInfo.Sid, room.Sid))

		updateTable = true
		ri.ID = roomDbInfo.ID
	}

	_, err = m.ds.InsertOrUpdateRoomInfo(ri)
	if err != nil {
		log.Errorln(fmt.Sprintf("error during data saving in db for %s, updateDb: %v, error: %s", r.RoomId, updateTable, err.Error()))
		return false, "Error: " + err.Error(), nil
	}

	// we'll silently add metadata into our redis
	// we can avoid errors (if occur) because it will update from webhook too
	_, _ = m.rs.ManageActiveRoomsWithMetadata(r.RoomId, "add", meta)

	return true, "room created", room
}

func (m *RoomModel) preRoomCreationTasks(r *plugnmeet.CreateRoomReq) {
	exist, err := m.rs.ManageActiveRoomsWithMetadata(r.GetRoomId(), "get", "")
	if err == nil && exist != nil {
		// maybe this room was ended just now, so we'll wait until clean up done
		waitFor := config.WaitBeforeTriggerOnAfterRoomEnded + (1 * time.Second)
		log.Infoln("this room:", r.GetRoomId(), "still active, we'll wait for:", waitFor, "before recreating it again.")
		time.Sleep(waitFor)
	}

	// check & wait
	m.CheckAndWaitUntilRoomCreationInProgress(r.GetRoomId())

	// we'll add this room in the processing list
	_, err = m.rs.RoomCreationProgressList(r.GetRoomId(), "add")
	if err != nil {
		log.Errorln(err)
	}
}

func (m *RoomModel) prepareWhiteboardPreloadFile(req *plugnmeet.CreateRoomReq, room *livekit.Room) {
	if !req.Metadata.RoomFeatures.WhiteboardFeatures.AllowedWhiteboard || req.Metadata.RoomFeatures.WhiteboardFeatures.PreloadFile == nil {
		return
	}

	// get file info
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Head(*req.Metadata.RoomFeatures.WhiteboardFeatures.PreloadFile)
	if err != nil {
		log.Errorln(err)
		return
	}

	if resp.ContentLength < 1 {
		log.Errorf("invalid file type")
		return
	} else if resp.ContentLength > config.MaxPreloadedWhiteboardFileSize {
		log.Errorf("Allowd %d but given %d", config.MaxPreloadedWhiteboardFileSize, resp.ContentLength)
		return
	}

	fm := filemodel.New(m.app, m.ds, m.rs, m.lk)
	cType := resp.Header.Get("Content-Type")
	if cType == "" {
		log.Errorln("invalid Content-Type")
		return
	}

	// validate file type
	mtype := mimetype.Lookup(cType)
	err = fm.ValidateMimeType(mtype)
	if err != nil {
		log.Errorln(err)
		return
	}

	downloadDir := fmt.Sprintf("%s/%s", config.GetConfig().UploadFileSettings.Path, room.Sid)
	if _, err = os.Stat(downloadDir); os.IsNotExist(err) {
		err = os.MkdirAll(downloadDir, os.ModePerm)
		if err != nil {
			log.Errorln(err)
			return
		}
	}

	// now download the file
	gres, err := grab.Get(downloadDir, *req.Metadata.RoomFeatures.WhiteboardFeatures.PreloadFile)
	if err != nil {
		log.Errorln(err)
		return
	}
	// double check to make sure that dangerous file wasn't uploaded
	mtype, err = mimetype.DetectFile(gres.Filename)
	if err != nil {
		log.Errorln(err)
		// remove the file if have problem
		_ = os.RemoveAll(gres.Filename)
		return
	}
	err = fm.ValidateMimeType(mtype)
	if err != nil {
		log.Errorln(err)
		// remove the file if validation not passed
		_ = os.RemoveAll(gres.Filename)
		return
	}

	ms := strings.SplitN(gres.Filename, "/", -1)
	fm.AddRequest(&filemodel.FileUploadReq{
		Sid:      room.Sid,
		RoomId:   room.Name,
		FilePath: fmt.Sprintf("%s/%s", room.Sid, ms[len(ms)-1]),
	})

	_, err = fm.ConvertWhiteboardFile()
	if err != nil {
		log.Errorln(err)
	}
	// finally, delete the file
	_ = os.RemoveAll(gres.Filename)
}
