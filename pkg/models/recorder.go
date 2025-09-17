package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

type RecorderModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService
	um          *UserModel
	logger      *logrus.Entry
}

func NewRecorderModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, natsService *natsservice.NatsService, um *UserModel, logger *logrus.Logger) *RecorderModel {
	return &RecorderModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsService,
		um:          um,
		logger:      logger.WithField("model", "recorder"),
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
		// in this case, we'll try to fetch the room info
		rmInfo, _ := m.ds.GetRoomInfoBySid(req.Sid, nil)
		if rmInfo == nil || rmInfo.IsRecording == 0 {
			return nil
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
		RecordingId: fmt.Sprintf("%s-%d", req.Sid, recordId),
	}

	switch req.Task {
	case plugnmeet.RecordingTasks_START_RECORDING:
		err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RecorderBot)
		if err != nil {
			return err
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		toSend.RtmpUrl = req.RtmpUrl
		err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RtmpBot)
		if err != nil {
			return err
		}
	}

	payload, err := proto.Marshal(toSend)
	if err != nil {
		return err
	}

	msg, err := m.app.NatsConn.RequestMsg(&nats.Msg{
		Subject: m.app.NatsInfo.Recorder.RecorderChannel,
		Data:    payload,
	}, time.Second*3)

	if err != nil {
		return err
	}

	res := new(plugnmeet.CommonResponse)
	if err = proto.Unmarshal(msg.Data, res); err == nil && !res.Status {
		return errors.New(res.GetMsg())
	}

	return nil
}
