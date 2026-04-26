package models

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

const recorderResponseTimeout = 3 * time.Second

func (m *RecordingModel) DispatchRecorderTask(req *plugnmeet.RecordingReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": req.RoomId,
		"sid":    req.Sid,
		"task":   req.Task.String(),
		"method": "DispatchRecorderTask",
	})

	if req.Task == plugnmeet.RecordingTasks_START_RECORDING || req.Task == plugnmeet.RecordingTasks_START_RTMP {
		// we'll use a lock to ensure that we're not sending multiple requests for the same task
		// we'll use a TTL of 30 seconds, which is more than enough for the recorder to respond
		lockKey := fmt.Sprintf(redisservice.RecorderTaskLockKey, req.RoomId, req.Task.String())
		lock := m.rs.NewLock(lockKey, 30*time.Second)

		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		acquired, err := lock.TryLock(ctx)
		if err != nil {
			log.WithError(err).Error("failed to acquire recorder task lock")
			return err
		}
		if !acquired {
			err := errors.New("another request is already in progress")
			log.WithError(err).Warn("recorder task lock already acquired")
			return err
		}
		defer lock.Unlock(ctx)
	}

	log.Infoln("Request to send message to recorder")

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
		if err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RecorderBot, log); err != nil {
			log.WithError(err).Error("failed to add token for recording bot")
			return err
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		toSend.RtmpUrl = req.RtmpUrl
		if err := m.addTokenAndRecorder(context.Background(), req, toSend, config.RtmpBot, log); err != nil {
			log.WithError(err).Error("Failed to add token for rtmp bot")
			return err
		}
	}

	payload, err := proto.Marshal(toSend)
	if err != nil {
		log.WithError(err).Error("Failed to marshal message for recorder")
		return err
	}

	log.Info("Sending request to NATS recorder channel")
	msg, err := m.app.NatsConn.RequestMsg(&nats.Msg{
		Subject: m.app.NatsInfo.Recorder.RecorderChannel,
		Data:    payload,
	}, recorderResponseTimeout)

	if err != nil {
		// is normal for plugnmeet.RecordingTasks_STOP not to receive any response from recorder if no task is running
		if req.Task == plugnmeet.RecordingTasks_STOP {
			log.Infof("Timed out waiting for a response from the NATS recorder channel after %s, which is expected when no task is active", recorderResponseTimeout)
		} else {
			log.WithError(err).Errorf("Timed out waiting for a response from the NATS recorder channel after %s", recorderResponseTimeout)
		}
		return err
	}

	res := new(plugnmeet.CommonResponse)
	if err = proto.Unmarshal(msg.Data, res); err != nil {
		log.WithError(err).Error("Failed to unmarshal response from recorder")
		return err
	}
	if !res.Status {
		err = errors.New(res.GetMsg())
		log.WithError(err).Error("Recorder returned a non-successful response")
		return err
	}

	log.Info("Successfully sent message to recorder and got a success response")
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

// calculateRecorderScore calculates a load score for a recorder. A lower score is better.
func calculateRecorderScore(r *utils.RecorderInfo) float64 {
	progressWeight := 0.6 // 60% weight for session progress
	cpuWeight := 0.4      // 40% weight for CPU load

	// 1. Calculate progress score (0-1)
	var progressScore float64
	if r.MaxLimit > 0 {
		progressScore = float64(r.CurrentProgress) / float64(r.MaxLimit)
	}
	// If MaxLimit is 0, progressScore remains 0, which is fine (no capacity defined).

	// 2. Get CPU score (0-1, already normalized by recorder)
	cpuScore := r.CpuScore

	// 3. Combine scores based on weights
	combinedScore := progressWeight*progressScore + cpuWeight*cpuScore

	// 4. Factor in total cores. A recorder with more cores can handle more load.
	// We penalize recorders with fewer cores by making their score higher.
	// Using a base of 2 cores to avoid over-penalizing single-core machines.
	if r.TotalCores > 0 {
		// Example: 2 cores -> factor 1.0; 4 cores -> factor 0.5; 1 core -> factor 2.0
		coreFactor := 2.0 / float64(r.TotalCores)
		combinedScore = combinedScore * coreFactor
	} else {
		// If TotalCores is not reported (older recorder), assume a base factor,
		// e.g., treat it as a 1-core machine for scoring purposes.
		combinedScore = combinedScore * 2.0 // Equivalent to 1 core (2.0 / 1.0)
	}

	return combinedScore
}

func (m *RecordingModel) selectRecorder(log *logrus.Entry) string {
	log = log.WithField("method", "selectRecorder")
	log.Info("Selecting a recorder")

	recorders := m.natsService.GetAllActiveRecorders()

	if len(recorders) < 1 {
		log.Warn("No active recorders found")
		return ""
	}

	// Sort recorders based on our new scoring logic. Lower score is better.
	sort.Slice(recorders, func(i, j int) bool {
		scoreI := calculateRecorderScore(recorders[i])
		scoreJ := calculateRecorderScore(recorders[j])
		return scoreI < scoreJ
	})

	// The best recorder is the first one in the sorted list.
	selected := recorders[0]
	log.WithFields(logrus.Fields{
		"selectedRecorderId": selected.RecorderId,
		"currentProgress":    selected.CurrentProgress,
		"maxLimit":           selected.MaxLimit,
		"cpuScore":           selected.CpuScore,
		"totalCores":         selected.TotalCores,
		"finalScore":         calculateRecorderScore(selected),
	}).Info("Successfully selected a recorder")
	return selected.RecorderId
}
