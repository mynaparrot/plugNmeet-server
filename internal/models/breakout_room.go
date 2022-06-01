package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
)

const breakoutRoomKey = "pnm:breakoutRoom:"

type breakoutRoom struct {
	ctx            context.Context
	rc             *redis.Client
	roomService    *RoomService
	roomAuthModel  *roomAuthModel
	authTokenModel *authTokenModel
}

func NewBreakoutRoomModel() *breakoutRoom {
	return &breakoutRoom{
		ctx:            context.Background(),
		rc:             config.AppCnf.RDS,
		roomService:    NewRoomService(),
		roomAuthModel:  NewRoomAuthModel(),
		authTokenModel: NewAuthTokenModel(),
	}
}

type CreateBreakoutRoomsReq struct {
	RoomId          string
	RequestedUserId string
	Duration        int64          `json:"duration"`
	Rooms           []BreakoutRoom `json:"rooms"`
}

type BreakoutRoom struct {
	Id       string             `json:"id"`
	Duration int64              `json:"duration"`
	Users    []BreakoutRoomUser `json:"users"`
}

type BreakoutRoomUser struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

func (m *breakoutRoom) CreateBreakoutRooms(r *CreateBreakoutRoomsReq) error {
	mainRoom, err := m.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return err
	}
	meta := new(RoomMetadata)
	err = json.Unmarshal([]byte(mainRoom.Metadata), meta)
	if err != nil {
		return err
	}
	// set room duration
	meta.Features.RoomDuration = r.Duration
	meta.IsBreakoutRoom = true

	// disable few features
	meta.Features.BreakoutRoomFeatures.IsAllow = false
	meta.Features.WaitingRoomFeatures.IsActive = false
	// we'll disable now. in the future, we can think about those
	meta.Features.AllowRecording = false
	meta.Features.AllowRTMP = false

	for _, room := range r.Rooms {
		bRoom := new(RoomCreateReq)
		bRoom.RoomId = fmt.Sprintf("%s:%s", r.RoomId, room.Id)
		bRoom.RoomMetadata = *meta
		status, msg, _ := m.roomAuthModel.CreateRoom(bRoom)

		if !status {
			log.Error(msg)
			continue
		}

		room.Duration = r.Duration
		marshal, err := json.Marshal(room)
		if err != nil {
			log.Error(err)
			continue
		}
		val := map[string]string{
			bRoom.RoomId: string(marshal),
		}
		pp := m.rc.Pipeline()
		pp.HSet(m.ctx, breakoutRoomKey+r.RoomId, val)
		_, err = pp.Exec(m.ctx)

		if err != nil {
			log.Error(err)
			continue
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.broadcastNotification(r.RoomId, r.RequestedUserId, u.Id, bRoom.RoomId, "JOIN_BREAKOUT_ROOM", false)
			if err != nil {
				log.Error(err)
				continue
			}
		}
	}
	// again here for update
	origMeta := new(RoomMetadata)
	err = json.Unmarshal([]byte(mainRoom.Metadata), origMeta)
	if err != nil {
		return err
	}
	origMeta.Features.BreakoutRoomFeatures.IsActive = true

	marshal, err := json.Marshal(origMeta)
	if err != nil {
		return err
	}
	_, err = m.roomService.UpdateRoomMetadata(r.RoomId, string(marshal))
	if err != nil {
		return err
	}

	return nil
}

type JoinBreakoutRoomReq struct {
	RoomId         string
	BreakoutRoomId string `json:"breakout_room_id"`
	UserId         string `json:"user_id"`
}

func (m *breakoutRoom) JoinBreakoutRoom(r *JoinBreakoutRoomReq) (string, error) {
	cmd := m.rc.HGet(m.ctx, breakoutRoomKey+r.RoomId, r.BreakoutRoomId)
	result, err := cmd.Result()
	if err != nil {
		return "", err
	}
	room := new(BreakoutRoom)
	err = json.Unmarshal([]byte(result), room)
	if err != nil {
		return "", err
	}

	canJoin := false
	for _, u := range room.Users {
		if u.Id == r.UserId {
			canJoin = true
		}
	}
	if !canJoin {
		return "", errors.New("you can't join in this room")
	}

	p, err := m.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return "", err
	}

	meta := new(UserMetadata)
	err = json.Unmarshal([]byte(p.Metadata), meta)
	if err != nil {
		return "", err
	}

	req := new(GenTokenReq)
	req.RoomId = r.BreakoutRoomId
	req.UserInfo.UserId = r.UserId
	req.UserInfo.Name = p.Name
	req.UserInfo.IsAdmin = meta.IsAdmin
	req.UserInfo.UserMetadata = *meta

	token, err := m.authTokenModel.DoGenerateToken(req)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *breakoutRoom) broadcastNotification(roomId, fromUserId, ToUserId, breakoutRoomId, mType string, isAdmin bool) error {
	payload := DataMessageRes{
		Type:   "SYSTEM",
		RoomId: roomId,
		To:     ToUserId,
		Body: DataMessageBody{
			Type: mType,
			From: ReqFrom{
				UserId: fromUserId,
			},
			Msg: breakoutRoomId,
		},
	}

	msg := WebsocketRedisMsg{
		Type:    "sendMsg",
		Payload: &payload,
		RoomId:  roomId,
		IsAdmin: isAdmin,
	}

	marshal, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	m.rc.Publish(m.ctx, "plug-n-meet-websocket", marshal)
	return nil
}
