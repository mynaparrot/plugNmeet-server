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
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"sid":    req.Sid,
		"task":   req.Task.String(),
		"method": "SendMsgToRecorder",
	})
	log.Infoln("request to send message to recorder")

	recordId := time.Now().UnixMilli()

	if req.RoomTableId == 0 {
		if req.Sid == "" {
			err := errors.New("empty sid")
			log.WithError(err).Error("roomTableId is 0 and sid is empty")
			return err
		}
		// in this case, we'll try to fetch the room info
		log.Info("roomTableId is 0, fetching room info by sid")
		rmInfo, _ := m.ds.GetRoomInfoBySid(req.Sid, nil)
		if rmInfo == nil || rmInfo.IsRecording == 0 {
			log.Warn("room not found by sid or is not in recording state, skipping")
			return nil
		}
		req.RoomTableId = int64(rmInfo.ID)
		req.RoomId = rmInfo.RoomId
		// update logger with correct roomId if it was missing
		log = log.WithField("roomId", req.RoomId)
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
		err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RecorderBot, log)
		if err != nil {
			log.WithError(err).Error("failed to add token for recording bot")
			return err
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		toSend.RtmpUrl = req.RtmpUrl
		err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RtmpBot, log)
		if err != nil {
			log.WithError(err).Error("failed to add token for rtmp bot")
			return err
		}
	}

	payload, err := proto.Marshal(toSend)
	if err != nil {
		log.WithError(err).Error("failed to marshal message for recorder")
		return err
	}

	log.Info("sending request to NATS recorder channel")
	msg, err := m.app.NatsConn.RequestMsg(&nats.Msg{
		Subject: m.app.NatsInfo.Recorder.RecorderChannel,
		Data:    payload,
	}, time.Second*3)

	if err != nil {
		log.WithError(err).Error("failed to get response from NATS recorder channel")
		return err
	}

	res := new(plugnmeet.CommonResponse)
	if err = proto.Unmarshal(msg.Data, res); err != nil {
		log.WithError(err).Error("failed to unmarshal response from recorder")
		return err
	}
	if !res.Status {
		err = errors.New(res.GetMsg())
		log.WithError(err).Error("recorder returned a non-successful response")
		return err
	}

	log.Info("successfully sent message to recorder and got a success response")
	return nil
}
