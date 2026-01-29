package models

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func (m *RecordingModel) DispatchRecorderTask(req *plugnmeet.RecordingReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"sid":    req.Sid,
		"task":   req.Task.String(),
		"method": "DispatchRecorderTask",
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
			log.Infoln("room not found by sid or is not in recording state, skipping")
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

func (m *RecordingModel) addTokenAndRecorder(ctx context.Context, req *plugnmeet.RecordingReq, rq *plugnmeet.PlugNmeetToRecorder, userId string, log *logrus.Entry) error {
	log = log.WithFields(logrus.Fields{
		"userId": userId,
		"method": "addTokenAndRecorder",
	})
	log.Info("adding token and selecting recorder")

	recorderId := m.selectRecorder(log)
	if recorderId == "" {
		err := fmt.Errorf("notifications.no-recorder-available")
		log.WithError(err).Error("no recorder available")
		return err
	}

	gt := &plugnmeet.GenerateTokenReq{
		RoomId: req.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:   userId,
			IsHidden: true,
			IsAdmin:  true,
		},
	}
	token, err := m.um.GetPNMJoinToken(ctx, gt)
	if err != nil {
		log.WithError(err).Errorln("error getting pnm token")
		return err
	}

	rq.RecorderId = recorderId
	rq.AccessToken = token

	// if we have custom design, then we'll set custom design with token
	// don't need to change anything in the recorder.
	if req.CustomDesign != nil && *req.CustomDesign != "" {
		log.Info("appending custom design to access token")
		rq.AccessToken += "&custom_design=" + url.QueryEscape(*req.CustomDesign)
	}

	log.WithField("recorderId", recorderId).Info("successfully added token and selected recorder")
	return nil
}

func (m *RecordingModel) selectRecorder(log *logrus.Entry) string {
	log = log.WithField("method", "selectRecorder")
	log.Info("selecting a recorder")

	recorders := m.natsService.GetAllActiveRecorders()

	if len(recorders) < 1 {
		log.Warn("no active recorders found")
		return ""
	}
	// let's sort it based on active processes & max limit.
	sort.Slice(recorders, func(i int, j int) bool {
		var iA, jA float64
		if recorders[i].MaxLimit > 0 {
			iA = float64(recorders[i].CurrentProgress) / float64(recorders[i].MaxLimit)
		}
		if recorders[j].MaxLimit > 0 {
			jA = float64(recorders[j].CurrentProgress) / float64(recorders[j].MaxLimit)
		}
		return iA < jA
	})

	// we'll return the first one
	selected := recorders[0]
	log.WithFields(logrus.Fields{
		"selectedRecorderId": selected.RecorderId,
		"currentProgress":    selected.CurrentProgress,
		"maxLimit":           selected.MaxLimit,
	}).Info("selected recorder")
	return selected.RecorderId
}
