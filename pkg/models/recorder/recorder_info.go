package recordermodel

import (
	"encoding/json"
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	usermodel "github.com/mynaparrot/plugnmeet-server/pkg/models/user"
	log "github.com/sirupsen/logrus"
	"net/url"
	"sort"
	"time"
)

func (m *RecorderModel) addTokenAndRecorder(req *plugnmeet.RecordingReq, rq *plugnmeet.PlugNmeetToRecorder, userId string) error {
	recorderId, err := m.selectRecorder()
	if err != nil {
		return err
	}
	if recorderId == "" {
		return errors.New("notifications.no-recorder-available")
	}

	gt := &plugnmeet.GenerateTokenReq{
		RoomId: req.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:   userId,
			IsHidden: true,
			IsAdmin:  true,
		},
	}
	um := usermodel.New(m.app, m.ds, m.rs, m.lk)
	token, err := um.GetPNMJoinToken(gt)
	if err != nil {
		log.Errorln(err)
		return err
	}

	rq.RecorderId = recorderId
	rq.AccessToken = token

	// if we have custom design then we'll set custom design with token
	// don't need to change anything in recorder.
	if req.CustomDesign != nil && *req.CustomDesign != "" {
		rq.AccessToken += "&custom_design=" + url.QueryEscape(*req.CustomDesign)
	}

	return nil
}

type recorderInfo struct {
	RecorderId      string
	MaxLimit        int   `json:"maxLimit"`
	CurrentProgress int   `json:"currentProgress"`
	LastPing        int64 `json:"lastPing"`
}

func (m *RecorderModel) getAllRecorders() ([]*recorderInfo, error) {
	result, err := m.rs.GetAllRecorders()
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("no recorder found")
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

func (m *RecorderModel) selectRecorder() (string, error) {
	recorders, err := m.getAllRecorders()
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
