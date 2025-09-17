package models

import (
	"context"
	"fmt"
	"net/url"
	"sort"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *RecorderModel) addTokenAndRecorder(ctx context.Context, req *plugnmeet.RecordingReq, rq *plugnmeet.PlugNmeetToRecorder, userId string) error {
	recorderId := m.selectRecorder()
	if recorderId == "" {
		return fmt.Errorf("notifications.no-recorder-available")
	}

	gt := &plugnmeet.GenerateTokenReq{
		RoomId: req.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:   userId,
			IsHidden: true,
			IsAdmin:  true,
		},
	}
	um := NewUserModel(m.app, m.ds, m.rs, m.logger.Logger)
	token, err := um.GetPNMJoinToken(ctx, gt)
	if err != nil {
		m.logger.WithError(err).Errorln("error getting pnm token")
		return err
	}

	rq.RecorderId = recorderId
	rq.AccessToken = token

	// if we have custom design, then we'll set custom design with token
	// don't need to change anything in the recorder.
	if req.CustomDesign != nil && *req.CustomDesign != "" {
		rq.AccessToken += "&custom_design=" + url.QueryEscape(*req.CustomDesign)
	}

	return nil
}

func (m *RecorderModel) selectRecorder() string {
	recorders := m.natsService.GetAllActiveRecorders()

	if len(recorders) < 1 {
		return ""
	}
	// let's sort it based on active processes & max limit.
	sort.Slice(recorders, func(i int, j int) bool {
		iA := (recorders[i].CurrentProgress) / recorders[i].MaxLimit
		jA := (recorders[j].CurrentProgress) / recorders[j].MaxLimit
		return iA < jA
	})

	// we'll return the first one
	return recorders[0].RecorderId
}
