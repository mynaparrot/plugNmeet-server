package models

import (
	"context"
	"errors"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"net/url"
	"sort"
	"strconv"
	"time"
)

type RecorderModel struct {
	app          *config.AppConfig
	rm           *RoomModel
	roomService  *RoomService
	rds          *redis.Client
	ctx          context.Context
	recordingReq *plugnmeet.RecordingReq
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

func NewRecorderModel() *RecorderModel {
	return &RecorderModel{
		app:         config.AppCnf,
		rm:          NewRoomModel(),
		roomService: NewRoomService(),
		rds:         config.AppCnf.RDS,
		ctx:         context.Background(),
	}
}

func (r *RecorderModel) SendMsgToRecorder(req *plugnmeet.RecordingReq) error {
	recordId := time.Now().UnixMilli()

	if req.RoomTableId == 0 {
		if req.Sid == "" {
			return errors.New("empty sid")
		}
		// in this case we'll try to fetch the room info
		rmInfo, msg := r.rm.GetRoomInfo("", req.Sid, 0)
		if rmInfo == nil {
			return errors.New(msg)
		}
		req.RoomTableId = rmInfo.Id
		req.RoomId = rmInfo.RoomId
	}

	r.recordingReq = req
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
		err := r.addTokenAndRecorder(toSend, config.RecorderBot)
		if err != nil {
			return err
		}
	case plugnmeet.RecordingTasks_START_RTMP:
		toSend.RtmpUrl = req.RtmpUrl
		err := r.addTokenAndRecorder(toSend, config.RtmpBot)
		if err != nil {
			return err
		}
	}

	payload, _ := protojson.Marshal(toSend)
	r.rds.Publish(r.ctx, "plug-n-meet-recorder", string(payload))

	return nil
}

func (r *RecorderModel) addTokenAndRecorder(rq *plugnmeet.PlugNmeetToRecorder, userId string) error {
	recorderId, err := r.selectRecorder()
	if err != nil {
		return err
	}
	if recorderId == "" {
		return errors.New("notifications.no-recorder-available")
	}

	m := NewAuthTokenModel()
	gt := &plugnmeet.GenerateTokenReq{
		RoomId: r.recordingReq.RoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:   userId,
			IsHidden: true,
			IsAdmin:  true,
		},
	}
	token, err := m.GeneratePlugNmeetAccessToken(gt)
	if err != nil {
		log.Errorln(err)
		return err
	}

	rq.RecorderId = recorderId
	rq.AccessToken = token

	// if we have custom design then we'll set custom design with token
	// don't need to change anything in recorder.
	if r.recordingReq.CustomDesign != nil && *r.recordingReq.CustomDesign != "" {
		rq.AccessToken += "&custom_design=" + url.QueryEscape(*r.recordingReq.CustomDesign)
	}

	return nil
}

type recorderInfo struct {
	RecorderId      string
	MaxLimit        int   `json:"maxLimit"`
	CurrentProgress int   `json:"currentProgress"`
	LastPing        int64 `json:"lastPing"`
}

func (r *RecorderModel) getAllRecorders() ([]*recorderInfo, error) {
	ctx := context.Background()
	res := r.rds.HGetAll(ctx, "pnm:recorders")
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

func (r *RecorderModel) selectRecorder() (string, error) {
	recorders, err := r.getAllRecorders()
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
