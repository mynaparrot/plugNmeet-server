package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
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

	From     string  `json:"from"`
	Task     string  `json:"task"`
	Status   bool    `json:"status"`
	Msg      string  `json:"msg"`
	RecordId string  `json:"record_id"`
	Sid      string  `json:"sid"`
	RoomId   string  `json:"room_id"`
	FilePath string  `json:"file_path"`
	FileSize float64 `json:"file_size"`
}

func (rm *recordingModel) HandleRecorderResp(r *plugnmeet.RecorderToPlugNmeet) {
	switch r.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		rm.recordingStarted(r)
		go rm.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_END_RECORDING:
		rm.recordingEnded(r)
		go rm.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_START_RTMP:
		rm.rtmpStarted(r)
		go rm.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_END_RTMP:
		rm.rtmpEnded(r)
		go rm.sendToWebhookNotifier(r)

	case plugnmeet.RecordingTasks_RECORDING_PROCEEDED:
		err := rm.addRecording(r)
		if err != nil {
			log.Errorln(err)
		}
		go rm.sendToWebhookNotifier(r)
	}
}

// recordingStarted update when recorder will start recording
func (rm *recordingModel) recordingStarted(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRecordingStatus(r, 1)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsRecording = true
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

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
func (rm *recordingModel) recordingEnded(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRecordingStatus(r, 0)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsRecording = false
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

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
func (rm *recordingModel) updateRoomRecordingStatus(r *plugnmeet.RecorderToPlugNmeet, isRecording int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_recording = ?, recorder_id = ? WHERE sid = ? OR sid = CONCAT(?, '-', id)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isRecording, r.RecorderId, r.RoomSid, r.RoomSid)
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
func (rm *recordingModel) rtmpStarted(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRTMPStatus(r, 1)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsActiveRTMP = true
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

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
func (rm *recordingModel) rtmpEnded(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRTMPStatus(r, 0)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsActiveRTMP = false
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

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
func (rm *recordingModel) updateRoomRTMPStatus(r *plugnmeet.RecorderToPlugNmeet, isActiveRtmp int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_active_rtmp = ?, rtmp_node_id = ? WHERE sid = ? OR sid = CONCAT(?, '-', id)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isActiveRtmp, r.RecorderId, r.RoomSid, r.RoomSid)
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

func (rm *recordingModel) addRecording(r *plugnmeet.RecorderToPlugNmeet) error {
	ri := NewRoomModel()
	roomInfo, _ := ri.GetRoomInfo("", r.RoomSid, 0)

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

	_, err = stmt.Exec(r.RecordingId, r.RoomId, roomInfo.Sid, r.RecorderId, r.FilePath, fmt.Sprintf("%.2f", r.FileSize), time.Now().Unix(), roomInfo.CreationTime)
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

func (rm *recordingModel) sendToWebhookNotifier(r *plugnmeet.RecorderToPlugNmeet) {
	n := NewWebhookNotifier()
	msg := CommonNotifyEvent{
		Event: r.Task.String(),
		Room: NotifyEventRoom{
			Sid:    r.RoomSid,
			RoomId: r.RoomId,
		},
		RecordingInfo: RecordingInfoEvent{
			RecordId:    r.RecordingId,
			RecorderId:  r.RecorderId,
			RecorderMsg: r.Msg,
			FilePath:    r.FilePath,
			FileSize:    float64(r.FileSize),
		},
	}

	err := n.Notify(r.RoomSid, msg)
	if err != nil {
		log.Errorln(err)
	}
}

type RecorderReq struct {
	From        string `json:"from"`
	Task        string `json:"task"`
	RoomId      string `json:"room_id"`
	Sid         string `json:"sid"`
	RecordId    string `json:"record_id"`
	AccessToken string `json:"access_token"`
	RecorderId  string `json:"recorder_id"`
	RtmpUrl     string `json:"rtmp_url"`
}

func (rm *recordingModel) SendMsgToRecorder(task string, roomId string, sid string, rtmpUrl string) error {
	recordId := time.Now().UnixMilli()

	toSend := &plugnmeet.PlugNmeetToRecorder{
		From:        "plugnmeet",
		RoomId:      roomId,
		RoomSid:     sid,
		RecordingId: sid + "-" + strconv.Itoa(int(recordId)),
	}

	switch task {
	case "start-recording":
		toSend.Task = plugnmeet.RecordingTasks_START_RECORDING
		err := rm.addTokenAndRecorder(toSend, "RECORDER_BOT")
		if err != nil {
			return err
		}
	case "stop-recording":
		toSend.Task = plugnmeet.RecordingTasks_STOP_RECORDING

	case "start-rtmp":
		toSend.Task = plugnmeet.RecordingTasks_START_RTMP
		toSend.RtmpUrl = &rtmpUrl
		err := rm.addTokenAndRecorder(toSend, "RTMP_BOT")
		if err != nil {
			return err
		}
	case "stop-rtmp":
		toSend.Task = plugnmeet.RecordingTasks_STOP_RTMP
	}

	payload, _ := protojson.Marshal(toSend)
	rm.rds.Publish(rm.ctx, "plug-n-meet-recorder", string(payload))

	return nil
}

func (rm *recordingModel) addTokenAndRecorder(rq *plugnmeet.PlugNmeetToRecorder, userId string) error {
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

type recorderInfo struct {
	RecorderId      string
	MaxLimit        int   `json:"maxLimit"`
	CurrentProgress int   `json:"currentProgress"`
	LastPing        int64 `json:"lastPing"`
}

func (rm *recordingModel) getAllRecorders() ([]*recorderInfo, error) {
	ctx := context.Background()
	res := rm.rds.HGetAll(ctx, "pnm:recorders")
	result, err := res.Result()
	if err != nil {
		return nil, err
	}

	var recorders []*recorderInfo
	valid := time.Now().Unix() - 8 // we can think maximum 8 seconds delay for valid node

	for id, data := range result {
		recorder := new(recorderInfo)
		err = json.Unmarshal([]byte(data), recorder)
		if err != nil {
			continue
		}
		if recorder.LastPing >= valid {
			recorder.RecorderId = id
			recorders = append(recorders, recorder)
		}
	}

	return recorders, err
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
		iA := (recorders[i].CurrentProgress) / recorders[i].MaxLimit
		jA := (recorders[j].CurrentProgress) / recorders[j].MaxLimit
		return iA < jA
	})

	// we'll return the first one
	return recorders[0].RecorderId, nil
}
