package models

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"os"
	"time"
)

type RecordingModel struct {
	app            *config.AppConfig
	db             *sql.DB
	roomService    *RoomService
	rds            *redis.Client
	ctx            context.Context
	analyticsModel *AnalyticsModel
}

func NewRecordingModel() *RecordingModel {
	return &RecordingModel{
		app:            config.AppCnf,
		db:             config.AppCnf.DB,
		roomService:    NewRoomService(),
		rds:            config.AppCnf.RDS,
		ctx:            context.Background(),
		analyticsModel: NewAnalyticsModel(),
	}
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

func (rm *RecordingModel) HandleRecorderResp(r *plugnmeet.RecorderToPlugNmeet, roomInfo *RoomInfo) {
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
		creation, err := rm.addRecording(r, roomInfo.CreationTime)
		if err != nil {
			log.Errorln(err)
		}
		// keep record of this file
		rm.addRecordingInfoFile(r, creation, roomInfo)
		go rm.sendToWebhookNotifier(r)
	}
}

// recordingStarted update when recorder will start recording
func (rm *RecordingModel) recordingStarted(r *plugnmeet.RecorderToPlugNmeet) {
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
	dm := NewDataMessageModel()
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: plugnmeet.DataMsgBodyType_INFO,
		Msg:         "notifications.recording-started",
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// recordingEnded will call when recorder will end recording
func (rm *RecordingModel) recordingEnded(r *plugnmeet.RecorderToPlugNmeet) {
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
	msgType := plugnmeet.DataMsgBodyType_INFO
	if !r.Status {
		msgType = plugnmeet.DataMsgBodyType_ALERT
		msg = "notifications.recording-ended-with-error"
	}
	// send message to room
	dm := NewDataMessageModel()
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: msgType,
		Msg:         msg,
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// updateRoomRecordingStatus to update recording status
func (rm *RecordingModel) updateRoomRecordingStatus(r *plugnmeet.RecorderToPlugNmeet, isRecording int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_recording = ?, recorder_id = ? WHERE id = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isRecording, r.RecorderId, r.RoomTableId)
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
func (rm *RecordingModel) rtmpStarted(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRTMPStatus(r, 1)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsActiveRtmp = true
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	// send message to room
	dm := NewDataMessageModel()
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: plugnmeet.DataMsgBodyType_INFO,
		Msg:         "notifications.rtmp-started",
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// rtmpEnded will call when recorder will end recording
func (rm *RecordingModel) rtmpEnded(r *plugnmeet.RecorderToPlugNmeet) {
	err := rm.updateRoomRTMPStatus(r, 0)
	if err != nil {
		log.Infoln(err)
	}

	// update room metadata
	_, roomMeta, err := rm.roomService.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return
	}

	roomMeta.IsActiveRtmp = false
	_, _ = rm.roomService.UpdateRoomMetadataByStruct(r.RoomId, roomMeta)

	msg := "notifications.rtmp-ended"
	msgType := plugnmeet.DataMsgBodyType_INFO
	if !r.Status {
		msgType = plugnmeet.DataMsgBodyType_ALERT
		msg = "notifications.rtmp-ended-with-error"
	}
	// send message to room
	dm := NewDataMessageModel()
	err = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: msgType,
		Msg:         msg,
		RoomId:      r.RoomId,
	})

	if err != nil {
		log.Errorln(err)
	}
}

// updateRoomRTMPStatus to update rtmp status
func (rm *RecordingModel) updateRoomRTMPStatus(r *plugnmeet.RecorderToPlugNmeet, isActiveRtmp int) error {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("UPDATE " + rm.app.FormatDBTable("room_info") + " SET is_active_rtmp = ?, rtmp_node_id = ? WHERE id = ?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(isActiveRtmp, r.RecorderId, r.RoomTableId)
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

func (rm *RecordingModel) addRecording(r *plugnmeet.RecorderToPlugNmeet, roomCreation int64) (int64, error) {
	db := rm.db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO " + rm.app.FormatDBTable("recordings") +
		" (record_id, room_id, room_sid, recorder_id, file_path, size, creation_time, room_creation_time) VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return 0, err
	}

	creation := time.Now().Unix()
	_, err = stmt.Exec(r.RecordingId, r.RoomId, r.RoomSid, r.RecorderId, r.FilePath, fmt.Sprintf("%.2f", r.FileSize), creation, roomCreation)
	if err != nil {
		return 0, err
	}
	err = tx.Commit()
	if err != nil {
		return 0, err
	}
	err = stmt.Close()
	if err != nil {
		return 0, err
	}

	return creation, nil
}

// addRecordingInfoFile will add information about recording file
// there have certain case that our DB may have problem
// using this recording info file we can import those recordings
// or will get idea about the recording
// format: path/recording_file_name.{mp4|webm}.json
func (rm *RecordingModel) addRecordingInfoFile(r *plugnmeet.RecorderToPlugNmeet, creation int64, roomInfo *RoomInfo) {
	var ended int64 = 0
	e, err := time.Parse("2006-01-02 15:04:05", roomInfo.Ended)
	if err == nil {
		ended = e.Unix()
		// this is indicating that the session is still running
		if ended < 1 {
			ended = 0
		}
	}

	toRecord := &plugnmeet.RecordingInfoFile{
		RoomTableId:      r.RoomTableId,
		RoomId:           r.RoomId,
		RoomTitle:        roomInfo.RoomTitle,
		RoomSid:          roomInfo.Sid,
		RoomCreationTime: roomInfo.CreationTime,
		RoomEnded:        ended,
		RecordingId:      r.RecordingId,
		RecorderId:       r.RecorderId,
		FilePath:         r.FilePath,
		FileSize:         r.FileSize,
		CreationTime:     creation,
	}
	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(toRecord)
	if err != nil {
		log.Errorln(err)
		return
	}
	path := fmt.Sprintf("%s/%s.json", config.AppCnf.RecorderInfo.RecordingFilesPath, r.FilePath)

	err = os.WriteFile(path, marshal, 0644)
	if err != nil {
		log.Errorln(err)
		return
	}
}

func (rm *RecordingModel) sendToWebhookNotifier(r *plugnmeet.RecorderToPlugNmeet) {
	tk := r.Task.String()
	n := GetWebhookNotifier(r.RoomId, r.RoomSid)
	if n != nil {
		msg := &plugnmeet.CommonNotifyEvent{
			Event: &tk,
			Room: &plugnmeet.NotifyEventRoom{
				Sid:    &r.RoomSid,
				RoomId: &r.RoomId,
			},
			RecordingInfo: &plugnmeet.RecordingInfoEvent{
				RecordId:    r.RecordingId,
				RecorderId:  r.RecorderId,
				RecorderMsg: r.Msg,
				FilePath:    &r.FilePath,
				FileSize:    &r.FileSize,
			},
		}

		err := n.SendWebhook(msg, nil)
		if err != nil {
			log.Errorln(err)
		}
	}

	// send analytics
	var val string
	data := &plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		RoomId:    r.RoomId,
	}

	switch r.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RECORDING_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String() + ":" + r.RecorderId
	case plugnmeet.RecordingTasks_END_RECORDING:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RECORDING_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	case plugnmeet.RecordingTasks_START_RTMP:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RTMP_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_STARTED.String() + ":" + r.RecorderId
	case plugnmeet.RecordingTasks_END_RTMP:
		data.EventName = plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_RTMP_STATUS
		val = plugnmeet.AnalyticsStatus_ANALYTICS_STATUS_ENDED.String()
	}
	data.HsetValue = &val
	rm.analyticsModel.HandleEvent(data)
}
