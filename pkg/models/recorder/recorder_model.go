package recordermodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"google.golang.org/protobuf/encoding/protojson"
	"strconv"
	"time"
)

type RecorderModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *RecorderModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
	}

	return &RecorderModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
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

func (m *RecorderModel) SendMsgToRecorder(req *plugnmeet.RecordingReq) error {
	recordId := time.Now().UnixMilli()

	if req.RoomTableId == 0 {
		if req.Sid == "" {
			return errors.New("empty sid")
		}
		// in this case we'll try to fetch the room info
		rmInfo, _ := m.ds.GetRoomInfoBySid(req.Sid, nil)
		if rmInfo == nil || rmInfo.IsRecording == 0 {
			return errors.New("room is not active")
		}
		req.RoomTableId = int64(rmInfo.ID)
		req.RoomId = rmInfo.RoomId
	}

	toSend := &plugnmeet.PlugNmeetToRecorder{
		From:        "plugnmeet",
		RoomTableId: req.RoomTableId,
		RoomId:      req.RoomId,
		RoomSid:     req.Sid,
		Task:        req.Task,
		RecordingId: req.Sid + "-" + strconv.Itoa(int(recordId)),
	}

	switch req.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		err := m.addTokenAndRecorder(req, toSend, config.RecorderBot)
		if err != nil {
			return err
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		toSend.RtmpUrl = req.RtmpUrl
		err := m.addTokenAndRecorder(req, toSend, config.RtmpBot)
		if err != nil {
			return err
		}
	}

	payload, _ := protojson.Marshal(toSend)
	return m.rs.PublishToRecorderChannel(string(payload))
}
