package models

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
	"net/url"
	"sort"
	"strconv"
	"time"
)

type recordingModel struct {
	app          *config.AppConfig
	db           *sql.DB
	roomService  *RoomService
	rds          *redis.Client
	ctx          context.Context
	RecordingReq *RecordingReq // we need to get custom design value
}

func NewRecordingModel() *recordingModel {
	return &recordingModel{
		app:         config.AppCnf,
		db:          config.AppCnf.DB,
		roomService: NewRoomService(),
		rds:         config.AppCnf.RDS,
		ctx:         context.Background(),
	}
}

type RecordingReq struct {
	Task         string  `json:"task" validate:"required"`
	Sid          string  `json:"sid" validate:"required"`
	RtmpUrl      string  `json:"rtmp_url"`
	CustomDesign *string `json:"custom_design,omitempty"`
}

func (rm *recordingModel) Validation(r *RecordingReq) []*config.ErrorResponse {
	return rm.app.DoValidateReq(r)
}

type RecorderResp struct {
	RecorderId string `json:"recorder_id"` //
	MaxLimit   int    `json:"max_limit"`

	ToServerId string  `json:"to_server_id"`
	From       string  `json:"from"`
	Task       string  `json:"task"`
	Status     bool    `json:"status"`
	Msg        string  `json:"msg"`
	RecordId   string  `json:"record_id"`
	Sid        string  `json:"sid"`
	RoomId     string  `json:"room_id"`
	FilePath   string  `json:"file_path"`
	FileSize   float64 `json:"file_size"`
}

func (rm *recordingModel) HandleRecorderResp(r *RecorderResp) {
	switch r.Task {
	case "recording-started":
		rm.recordingStarted(r)
		rm.sendToWebhookNotifier(r)

	case "recording-ended":
		rm.recordingEnded(r)
		rm.sendToWebhookNotifier(r)

	case "rtmp-started":
		rm.rtmpStarted(r)
		rm.sendToWebhookNotifier(r)

	case "rtmp-ended":
		rm.rtmpEnded(r)
		rm.sendToWebhookNotifier(r)

	case "recording-proceeded":
		err := rm.addRecording(r)
		if err != nil {
			log.Errorln(err)
		}
		rm.sendToWebhookNotifier(r)
	case "addRecorder":
		err := rm.addRecorder(r)
		if err != nil {
			log.Errorln(err)
		}
	case "ping":
		err := rm.addRecorderPing(r)
		if err != nil {
			log.Errorln(err)
		}
	}
}

// recordingStarted update when recorder will start recording
func (rm *recordingModel) recordingStarted(r *RecorderResp) {
	err := rm.updateRoomRecordingStatus(r, 1)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	room, err := rm.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return
	}

	m := make([]byte, len(room.Metadata))
	copy(m, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(m, roomMeta)
	roomMeta.IsRecording = true

	metadata, _ := json.Marshal(roomMeta)
	_, _ = rm.roomService.UpdateRoomMetadata(r.RoomId, string(metadata))

	// send message to room
	err = NewDataMessage(&DataMessageReq{
		MsgType: "INFO",
		Msg:     "notifications.recording-started",
		RoomId:  r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// recordingEnded will call when recorder will end recording
func (rm *recordingModel) recordingEnded(r *RecorderResp) {
	err := rm.updateRoomRecordingStatus(r, 0)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	room, err := rm.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return
	}

	m := make([]byte, len(room.Metadata))
	copy(m, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(m, roomMeta)
	roomMeta.IsRecording = false

	metadata, _ := json.Marshal(roomMeta)
	_, _ = rm.roomService.UpdateRoomMetadata(r.RoomId, string(metadata))

	msg := "notifications.recording-ended"
	msgType := "INFO"
	if !r.Status {
		msgType = "ALERT"
		msg = "notifications.recording-ended-with-error"
	}
	// send message to room
	err = NewDataMessage(&DataMessageReq{
		MsgType: msgType,
		Msg:     msg,
		RoomId:  r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// updateRoomRecordingStatus to update recording status
func (rm *recordingModel) updateRoomRecordingStatus(r *RecorderResp, isRecording int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_recording = ?, recorder_id = ? WHERE sid = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isRecording, r.RecorderId, r.Sid)
	if err != nil {
		return err
	}

	// update recorder table too
	q := "UPDATE " + rm.app.FormatDBTable("recorder") + " SET current_progress = current_progress + 1 WHERE recorder_id = ?"
	if isRecording == 0 {
		q = "UPDATE " + rm.app.FormatDBTable("recorder") + " SET current_progress = current_progress - 1 WHERE recorder_id = ? AND current_progress > 0"
	}
	stmt, err = tx.Prepare(q)
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.RecorderId)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	err = stmt.Close()
	if err != nil {
		return err
	}

	return nil
}

// rtmpStarted will call when rtmp has been started
func (rm *recordingModel) rtmpStarted(r *RecorderResp) {
	err := rm.updateRoomRTMPStatus(r, 1)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	room, err := rm.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return
	}

	m := make([]byte, len(room.Metadata))
	copy(m, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(m, roomMeta)
	roomMeta.IsActiveRTMP = true

	metadata, _ := json.Marshal(roomMeta)
	_, _ = rm.roomService.UpdateRoomMetadata(r.RoomId, string(metadata))

	// send message to room
	err = NewDataMessage(&DataMessageReq{
		MsgType: "INFO",
		Msg:     "notifications.rtmp-started",
		RoomId:  r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// rtmpEnded will call when recorder will end recording
func (rm *recordingModel) rtmpEnded(r *RecorderResp) {
	err := rm.updateRoomRTMPStatus(r, 0)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	room, err := rm.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return
	}

	m := make([]byte, len(room.Metadata))
	copy(m, room.Metadata)

	roomMeta := new(RoomMetadata)
	_ = json.Unmarshal(m, roomMeta)
	roomMeta.IsActiveRTMP = false

	metadata, _ := json.Marshal(roomMeta)
	_, _ = rm.roomService.UpdateRoomMetadata(r.RoomId, string(metadata))

	msg := "notifications.rtmp-ended"
	msgType := "INFO"
	if !r.Status {
		msgType = "ALERT"
		msg = "notifications.rtmp-ended-with-error"
	}
	// send message to room
	err = NewDataMessage(&DataMessageReq{
		MsgType: msgType,
		Msg:     msg,
		RoomId:  r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// updateRoomRTMPStatus to update rtmp status
func (rm *recordingModel) updateRoomRTMPStatus(r *RecorderResp, isActiveRtmp int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_active_rtmp = ?, rtmp_node_id = ? WHERE sid = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isActiveRtmp, r.RecorderId, r.Sid)
	if err != nil {
		return err
	}

	// update recorder table too
	q := "UPDATE " + rm.app.FormatDBTable("recorder") + " SET current_progress = current_progress + 1 WHERE recorder_id = ?"
	if isActiveRtmp == 0 {
		q = "UPDATE " + rm.app.FormatDBTable("recorder") + " SET current_progress = current_progress - 1 WHERE recorder_id = ? AND current_progress > 0"
	}
	stmt, err = tx.Prepare(q)
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.RecorderId)
	if err != nil {
		return err
	}

	err = tx.Commit()
	if err != nil {
		return err
	}

	err = stmt.Close()
	if err != nil {
		return err
	}

	return nil
}

// addRecorder will call when new recorder will join in redis channel
func (rm *recordingModel) addRecorder(r *RecorderResp) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO " + rm.app.FormatDBTable("recorder") + " (recorder_id, max_limit) VALUES (?, ?) ON DUPLICATE KEY UPDATE max_limit = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.RecorderId, r.MaxLimit, r.MaxLimit)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}
	err = stmt.Close()
	if err != nil {
		return err
	}

	return nil
}

type recorderInfo struct {
	recorderId      string
	maxLimit        int
	currentProgress int
}

func (rm *recordingModel) getAllRecorders() ([]*recorderInfo, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	valid := (time.Now().Unix() - 11) // current time + 11 seconds
	rows, err := db.QueryContext(ctx, "SELECT recorder_id, max_limit, current_progress FROM "+rm.app.FormatDBTable("recorder ")+" WHERE last_ping >= ?", valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recorder := new(recorderInfo)
	var recorders []*recorderInfo

	for rows.Next() {
		err = rows.Scan(&recorder.recorderId, &recorder.maxLimit, &recorder.currentProgress)
		if err != nil {
			fmt.Println(err)
		}
		recorders = append(recorders, recorder)
	}

	return recorders, nil
}

func (rm *recordingModel) addRecorderPing(r *RecorderResp) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("recorder") + " SET last_ping = ? WHERE recorder_id = ?")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(time.Now().Unix(), r.RecorderId)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}
	err = stmt.Close()
	if err != nil {
		return err
	}
	return nil
}

func (rm *recordingModel) updateRecorderCurrentProgress(r *RecorderResp) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("recorder") + " SET current_progress = current_progress + 1 WHERE recorder_id = ?")
	if err != nil {
		return err
	}
	_, err = stmt.Exec(r.RecorderId)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}
	err = stmt.Close()
	if err != nil {
		return err
	}
	return nil
}

func (rm *recordingModel) addRecording(r *RecorderResp) error {
	ri := NewRoomModel()
	roomInfo, _ := ri.GetRoomInfo("", r.Sid, 0)

	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO " + rm.app.FormatDBTable("recordings") +
		" (record_id, room_id, room_sid, recorder_id, file_path, size, creation_time, room_creation_time) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(r.RecordId, r.RoomId, r.Sid, r.RecorderId, r.FilePath, r.FileSize, time.Now().Unix(), roomInfo.CreationTime)
	if err != nil {
		return err
	}
	err = tx.Commit()
	if err != nil {
		return err
	}
	err = stmt.Close()
	if err != nil {
		return err
	}

	return nil
}

func (rm *recordingModel) sendToWebhookNotifier(r *RecorderResp) {
	n := NewWebhookNotifier()
	msg := CommonNotifyEvent{
		Event: r.Task,
		Room: NotifyEventRoom{
			Sid:    r.Sid,
			RoomId: r.RoomId,
		},
		RecordingInfo: RecordingInfoEvent{
			RecordId:    r.RecordId,
			RecorderId:  r.RecorderId,
			RecorderMsg: r.Msg,
			FilePath:    r.FilePath,
			FileSize:    r.FileSize,
		},
	}

	err := n.Notify(r.Sid, msg)
	if err != nil {
		log.Errorln(err)
	}
}

type RecorderReq struct {
	From         string `json:"from"`
	FromServerId string `json:"from_server_id"`
	Task         string `json:"task"`
	RoomId       string `json:"room_id"`
	Sid          string `json:"sid"`
	RecordId     string `json:"record_id"`
	AccessToken  string `json:"access_token"`
	RecorderId   string `json:"recorder_id"`
	RtmpUrl      string `json:"rtmp_url"`
}

func (rm *recordingModel) SendMsgToRecorder(task string, roomId string, sid string, rtmpUrl string) error {
	recordId := time.Now().UnixMilli()

	toSend := &RecorderReq{
		From:         "plugnmeet",
		FromServerId: config.AppCnf.Client.ServerId,
		Task:         task,
		RoomId:       roomId,
		Sid:          sid,
		RecordId:     sid + "-" + strconv.Itoa(int(recordId)),
	}
	switch task {
	case "start-recording":
		err := rm.addTokenAndRecorder(toSend, "RECORDER_BOT")
		if err != nil {
			return err
		}
	case "stop-recording":
		toSend.Task = "stop-recording"

	case "start-rtmp":
		toSend.RtmpUrl = rtmpUrl
		err := rm.addTokenAndRecorder(toSend, "RTMP_BOT")
		if err != nil {
			return err
		}
	case "stop-rtmp":
		toSend.Task = "stop-rtmp"
	}

	payload, _ := json.Marshal(toSend)
	rm.rds.Publish(rm.ctx, "plug-n-meet-recorder", string(payload))

	return nil
}

func (rm *recordingModel) addTokenAndRecorder(rq *RecorderReq, userId string) error {
	recorderId, err := rm.selectRecorder()
	if err != nil {
		return err
	}
	if recorderId == "" {
		return errors.New("notifications.no-recorder-available")
	}

	m := NewAuthTokenModel()
	gt := &GenTokenReq{
		rq.RoomId,
		UserInfo{
			UserId:   userId,
			IsHidden: true,
			IsAdmin:  true,
		},
	}
	token, err := m.DoGenerateToken(gt)
	if err != nil {
		log.Errorln(err)
		return err
	}

	rq.RecorderId = recorderId
	rq.AccessToken = token

	// if we have custom design then we'll set custom design with token
	// don't need to change anything in recorder.
	if rm.RecordingReq.CustomDesign != nil && *rm.RecordingReq.CustomDesign != "" {
		rq.AccessToken += "&custom_design=" + url.QueryEscape(*rm.RecordingReq.CustomDesign)
	}

	return nil
}

func (rm *recordingModel) selectRecorder() (string, error) {
	recorders, err := rm.getAllRecorders()
	if err != nil {
		return "", err
	}
	if len(recorders) < 1 {
		return "", nil
	}
	// let's sort it based on active processes & max limit.
	sort.Slice(recorders, func(i int, j int) bool {
		iA := (recorders[i].currentProgress) / recorders[i].maxLimit
		jA := (recorders[j].currentProgress) / recorders[j].maxLimit
		return iA < jA
	})

	// we'll return the first one
	return recorders[0].recorderId, nil
}
