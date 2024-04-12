package models

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/cavaliergopher/grab/v3"
	"github.com/gabriel-vasile/mimetype"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"time"
)

type RoomAuthModel struct {
	rs *RoomService
	rm *RoomModel
}

func NewRoomAuthModel() *RoomAuthModel {
	return &RoomAuthModel{
		rs: NewRoomService(),
		rm: NewRoomModel(),
	}
}

func (am *RoomAuthModel) CreateRoom(r *plugnmeet.CreateRoomReq) (bool, string, *livekit.Room) {
	// some pre creation tasks
	am.preRoomCreationTasks(r)

	// in preRoomCreationTasks we've added this room in progress list
	// so, we'll just use defer to clean this room at the end of this function
	defer am.rs.RoomCreationProgressList(r.RoomId, "del")

	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)
	if roomDbInfo.Id > 0 {
		rf, err := am.rs.LoadRoomInfo(r.RoomId)
		if err != nil && err.Error() != config.RequestedRoomNotExist {
			return false, "can't create room. try again", nil
		}

		if err == nil && rf.Sid == roomDbInfo.Sid {
			return true, "room already exists", rf
		}

		// we'll allow creating room again & use the same DB row
		// we can just update the DB row. No need to create a new one
	}

	// we'll set default values otherwise client got confused if data is missing
	utils.PrepareDefaultRoomFeatures(r)
	utils.SetCreateRoomDefaultValues(r, config.AppCnf.UploadFileSettings.MaxSize, config.AppCnf.UploadFileSettings.AllowedTypes, config.AppCnf.SharedNotePad.Enabled)
	utils.SetRoomDefaultLockSettings(r)
	// set default room settings
	utils.SetDefaultRoomSettings(config.AppCnf.RoomDefaultSettings, r)

	// copyright
	if config.AppCnf.Client.CopyrightConf == nil {
		r.Metadata.CopyrightConf = &plugnmeet.CopyrightConf{
			Display: true,
			Text:    "Powered by <a href=\"https://www.plugnmeet.org\" target=\"_blank\">plugNmeet</a>",
		}
	} else {
		r.Metadata.CopyrightConf = config.AppCnf.Client.CopyrightConf
	}

	// Azure cognitive services
	azu := config.AppCnf.AzureCognitiveServicesSpeech
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

	meta, err := am.rs.MarshalRoomMetadata(r.Metadata)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	room, err := am.rs.CreateRoom(r.RoomId, r.EmptyTimeout, r.MaxParticipants, meta)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	if room.Sid == "" {
		// without SID, it is hard to manage, if empty then we won't continue
		// in this case we'll end the room to clean up
		_, _ = am.rs.EndRoom(r.RoomId)
		return false, "Error: can't create room with empty SID", nil
	}

	isBreakoutRoom := 0
	if r.Metadata.IsBreakoutRoom {
		isBreakoutRoom = 1
	} else {
		// at present, we'll fetch file for main room only
		go am.prepareWhiteboardPreloadFile(r, room)
	}

	updateTable := false
	ri := &RoomInfo{
		RoomTitle:          r.Metadata.RoomTitle,
		RoomId:             room.Name,
		Sid:                room.Sid,
		JoinedParticipants: 0,
		IsRunning:          1,
		CreationTime:       room.CreationTime,
		Created:            time.Now().UTC().Format("2006-01-02 15:04:05"),
		WebhookUrl:         "",
		IsBreakoutRoom:     int64(isBreakoutRoom),
		ParentRoomId:       r.Metadata.ParentRoomId,
	}
	if r.Metadata.WebhookUrl != nil {
		ri.WebhookUrl = *r.Metadata.WebhookUrl
	}

	if roomDbInfo.Id > 0 {
		updateTable = true
		ri.Id = roomDbInfo.Id
	}

	_, err = am.rm.InsertOrUpdateRoomData(ri, updateTable)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	// we'll silently add metadata into our redis
	// we can avoid errors (if occur) because it will update from webhook too
	_, _ = am.rs.ManageActiveRoomsWithMetadata(r.RoomId, "add", meta)

	return true, "room created", room
}

func (am *RoomAuthModel) preRoomCreationTasks(r *plugnmeet.CreateRoomReq) {
	exist, err := am.rs.ManageActiveRoomsWithMetadata(r.GetRoomId(), "get", "")
	if err == nil && exist != nil {
		// maybe this room was ended just now, so we'll wait until clean up done
		waitFor := config.WaitBeforeTriggerOnAfterRoomEnded + (1 * time.Second)
		log.Infoln("this room:", r.GetRoomId(), "still active, we'll wait for:", waitFor, "before recreating it again.")
		time.Sleep(waitFor)
	}

	for {
		list, err := am.rs.RoomCreationProgressList(r.GetRoomId(), "exist")
		if err != nil {
			log.Errorln(err)
			break
		}
		if list {
			log.Println(r.GetRoomId(), "creation in progress, so waiting for", config.WaitDurationIfRoomInProgress)
			// we'll wait
			time.Sleep(config.WaitDurationIfRoomInProgress)
		} else {
			break
		}
	}

	// we'll add this room in processing list
	_, err = am.rs.RoomCreationProgressList(r.RoomId, "add")
	if err != nil {
		log.Errorln(err)
	}
}

func (am *RoomAuthModel) IsRoomActive(r *plugnmeet.IsRoomActiveReq) (bool, string, *plugnmeet.RoomMetadata) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room is not active", nil
	}

	// let's make sure room actually active
	_, meta, err := am.rs.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		// room isn't active. Change status
		_, _ = am.rm.UpdateRoomStatus(&RoomInfo{
			RoomId:    r.RoomId,
			IsRunning: 0,
			Ended:     time.Now().UTC().Format("2006-01-02 15:04:05"),
		})
		return false, "room is not active", nil
	}

	return true, "room is active", meta
}

func (am *RoomAuthModel) GetActiveRoomInfo(r *plugnmeet.GetActiveRoomInfoReq) (bool, string, *plugnmeet.ActiveRoomWithParticipant) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "no room found", nil
	}

	rrr, err := am.rs.LoadRoomInfo(r.RoomId)
	if err != nil {
		return false, err.Error(), nil
	}

	res := new(plugnmeet.ActiveRoomWithParticipant)
	res.RoomInfo = &plugnmeet.ActiveRoomInfo{
		RoomTitle:          roomDbInfo.RoomTitle,
		RoomId:             roomDbInfo.RoomId,
		Sid:                roomDbInfo.Sid,
		JoinedParticipants: roomDbInfo.JoinedParticipants,
		IsRunning:          int32(roomDbInfo.IsRunning),
		IsRecording:        int32(roomDbInfo.IsRecording),
		IsActiveRtmp:       int32(roomDbInfo.IsActiveRTMP),
		WebhookUrl:         roomDbInfo.WebhookUrl,
		IsBreakoutRoom:     int32(roomDbInfo.IsBreakoutRoom),
		ParentRoomId:       roomDbInfo.ParentRoomId,
		CreationTime:       roomDbInfo.CreationTime,
		Metadata:           rrr.Metadata,
	}
	res.ParticipantsInfo, _ = am.rs.LoadParticipants(roomDbInfo.RoomId)

	return true, "success", res
}

func (am *RoomAuthModel) GetActiveRoomsInfo() (bool, string, []*plugnmeet.ActiveRoomWithParticipant) {
	roomsInfo, err := am.rm.GetActiveRoomsInfo()

	if err != nil {
		return false, "no active room found", nil
	}

	if len(roomsInfo) == 0 {
		return false, "no active room found", nil
	}

	var res []*plugnmeet.ActiveRoomWithParticipant
	for _, r := range roomsInfo {
		roomInfo := r
		i := new(plugnmeet.ActiveRoomWithParticipant)
		i.RoomInfo = roomInfo

		participants, err := am.rs.LoadParticipants(r.RoomId)
		if err == nil {
			i.ParticipantsInfo = participants
		}

		rri, err := am.rs.LoadRoomInfo(r.RoomId)
		if err == nil {
			i.RoomInfo.Metadata = rri.Metadata
		}

		res = append(res, i)
	}

	return true, "success", res
}

func (am *RoomAuthModel) EndRoom(r *plugnmeet.RoomEndReq) (bool, string) {
	roomDbInfo, _ := am.rm.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room not active"
	}

	_, err := am.rs.EndRoom(r.RoomId)
	if err != nil {
		return false, "can't end room"
	}

	_, _ = am.rm.UpdateRoomStatus(&RoomInfo{
		RoomId:    r.RoomId,
		IsRunning: 0,
		Ended:     time.Now().UTC().Format("2006-01-02 15:04:05"),
	})

	return true, "success"
}

func (am *RoomAuthModel) FetchPastRooms(r *plugnmeet.FetchPastRoomsReq) (*plugnmeet.FetchPastRoomsResult, error) {
	db := config.AppCnf.DB
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	limit := r.Limit
	orderBy := "DESC"

	if limit == 0 {
		limit = 20
	}
	if r.OrderBy == "ASC" {
		orderBy = "ASC"
	}

	var rows *sql.Rows
	var err error

	switch {
	case len(r.RoomIds) > 0:
		var args []interface{}
		for _, rd := range r.RoomIds {
			args = append(args, rd)
		}
		args = append(args, r.From)
		args = append(args, limit)

		query := "SELECT a.room_title, a.roomId, a.sid, a.joined_participants, a.webhook_url, a.created, a.ended, b.file_id FROM " + config.AppCnf.FormatDBTable("room_info") + " AS a LEFT JOIN " + config.AppCnf.FormatDBTable("room_analytics") + " AS b ON a.id = b.room_table_id WHERE a.roomId IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ") AND a.is_running = '0' ORDER BY a.id " + orderBy + " LIMIT ?,?"

		rows, err = db.QueryContext(ctx, query, args...)
	default:
		rows, err = db.QueryContext(ctx, "SELECT a.room_title, a.roomId, a.sid, a.joined_participants, a.webhook_url, a.created, a.ended, b.file_id FROM  "+config.AppCnf.FormatDBTable("room_info")+" AS a LEFT JOIN "+config.AppCnf.FormatDBTable("room_analytics")+" AS b ON a.id = b.room_table_id WHERE a.is_running = '0' ORDER BY a.id "+orderBy+" LIMIT ?,?", r.From, limit)
	}

	if err != nil {
		return nil, err
	}

	defer rows.Close()
	var rooms []*plugnmeet.PastRoomInfo

	for rows.Next() {
		var room plugnmeet.PastRoomInfo
		var rSid, analyticsFileId sql.NullString

		err = rows.Scan(&room.RoomTitle, &room.RoomId, &rSid, &room.JoinedParticipants, &room.WebhookUrl, &room.Created, &room.Ended, &analyticsFileId)
		if err != nil {
			log.Errorln(err)
		}
		room.RoomSid = rSid.String
		room.AnalyticsFileId = analyticsFileId.String
		rooms = append(rooms, &room)
	}

	// get total number of recordings
	var row *sql.Row
	switch {
	case len(r.RoomIds) > 0:
		var args []interface{}
		for _, rd := range r.RoomIds {
			args = append(args, rd)
		}
		query := "SELECT COUNT(*) AS total FROM " + config.AppCnf.FormatDBTable("room_info") + " WHERE roomId IN (?" + strings.Repeat(",?", len(r.RoomIds)-1) + ") AND is_running = '0'"
		row = db.QueryRowContext(ctx, query, args...)
	default:
		row = db.QueryRowContext(ctx, "SELECT COUNT(*) AS total FROM "+config.AppCnf.FormatDBTable("room_info")+" WHERE is_running = '0'")
	}

	var total int64
	_ = row.Scan(&total)

	result := &plugnmeet.FetchPastRoomsResult{
		TotalRooms: total,
		From:       r.From,
		Limit:      limit,
		OrderBy:    orderBy,
		RoomsList:  rooms,
	}

	return result, nil
}

func (am *RoomAuthModel) ChangeVisibility(r *plugnmeet.ChangeVisibilityRes) (bool, string) {
	_, roomMeta, err := am.rs.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return false, err.Error()
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.RoomFeatures.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.RoomFeatures.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	_, err = am.rs.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}

func (am *RoomAuthModel) prepareWhiteboardPreloadFile(req *plugnmeet.CreateRoomReq, room *livekit.Room) {
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

	fm := NewManageFileModel(&ManageFile{
		Sid:    room.Sid,
		RoomId: room.Name,
	})

	cType := resp.Header.Get("Content-Type")
	if cType == "" {
		log.Errorln("invalid Content-Type")
		return
	}

	// validate file type
	mtype := mimetype.Lookup(cType)
	err = fm.validateMimeType(mtype)
	if err != nil {
		log.Errorln(err)
		return
	}

	downloadDir := fmt.Sprintf("%s/%s", config.AppCnf.UploadFileSettings.Path, room.Sid)
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
	err = fm.validateMimeType(mtype)
	if err != nil {
		log.Errorln(err)
		// remove the file if validation not passed
		_ = os.RemoveAll(gres.Filename)
		return
	}

	ms := strings.SplitN(gres.Filename, "/", -1)
	fm.FilePath = fmt.Sprintf("%s/%s", room.Sid, ms[len(ms)-1])

	_, err = fm.ConvertWhiteboardFile()
	if err != nil {
		log.Errorln(err)
	}
	// finally delete the file
	_ = os.RemoveAll(gres.Filename)
}
