package models

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RecorderModel) addTokenAndRecorder(ctx context.Context, req *plugnmeet.RecordingReq, rq *plugnmeet.PlugNmeetToRecorder, userId string, log *logrus.Entry) error {
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

func (m *RecorderModel) selectRecorder(log *logrus.Entry) string {
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
