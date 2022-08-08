package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
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
	Duration        uint64          `json:"duration" validate:"required"`
	WelcomeMsg      string          `json:"welcome_msg"`
	Rooms           []*BreakoutRoom `json:"rooms" validate:"required"`
}

type BreakoutRoom struct {
	Id       string              `json:"id"`
	Title    string              `json:"title"`
	Duration uint64              `json:"duration"`
	Started  bool                `json:"started"`
	Created  int64               `json:"created"`
	Users    []*BreakoutRoomUser `json:"users"`
}

type BreakoutRoomUser struct {
	Id     string `json:"id"`
	Name   string `json:"name"`
	Joined bool   `json:"joined"`
}

func (m *breakoutRoom) CreateBreakoutRooms(r *CreateBreakoutRoomsReq) error {
	mainRoom, err := m.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return err
	}
	meta := new(plugnmeet.RoomMetadata)
	err = json.Unmarshal([]byte(mainRoom.Metadata), meta)
	if err != nil {
		return err
	}
	// set room duration
	meta.RoomFeatures.RoomDuration = &r.Duration
	meta.IsBreakoutRoom = true
	meta.WelcomeMessage = &r.WelcomeMsg
	meta.ParentRoomId = r.RoomId

	// disable few features
	meta.RoomFeatures.BreakoutRoomFeatures.IsAllow = false
	meta.RoomFeatures.WaitingRoomFeatures.IsActive = false

	// we'll disable now. in the future, we can think about those
	meta.RoomFeatures.AllowRecording = false
	meta.RoomFeatures.AllowRtmp = false

	// clear few main room data
	meta.RoomFeatures.DisplayExternalLinkFeatures.IsActive = false
	meta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive = false

	e := make(map[string]bool)

	for _, room := range r.Rooms {
		bRoom := new(plugnmeet.CreateRoomReq)
		bRoom.RoomId = fmt.Sprintf("%s:%s", r.RoomId, room.Id)
		meta.RoomTitle = room.Title
		bRoom.Metadata = meta
		status, msg, _ := m.roomAuthModel.CreateRoom(bRoom)

		if !status {
			log.Error(msg)
			e[bRoom.RoomId] = true
			continue
		}

		room.Duration = r.Duration
		room.Created = time.Now().Unix()
		marshal, err := json.Marshal(room)

		if err != nil {
			log.Error(err)
			e[bRoom.RoomId] = true
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
			e[bRoom.RoomId] = true
			continue
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.broadcastNotification(r.RoomId, r.RequestedUserId, u.Id, bRoom.RoomId, plugnmeet.DataMsgType_SYSTEM, plugnmeet.DataMsgBodyType_JOIN_BREAKOUT_ROOM, false)
			if err != nil {
				log.Error(err)
				continue
			}
		}
	}

	if len(e) == len(r.Rooms) {
		return errors.New("breakout room creation wasn't successful")
	}

	// again here for update
	origMeta := new(plugnmeet.RoomMetadata)
	err = json.Unmarshal([]byte(mainRoom.Metadata), origMeta)
	if err != nil {
		return err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	_, err = m.roomService.UpdateRoomMetadataByStruct(r.RoomId, origMeta)

	return err
}

type JoinBreakoutRoomReq struct {
	RoomId         string
	BreakoutRoomId string `json:"breakout_room_id" validate:"required"`
	UserId         string `json:"user_id" validate:"required"`
	IsAdmin        bool   `json:"-"`
}

func (m *breakoutRoom) JoinBreakoutRoom(r *JoinBreakoutRoomReq) (string, error) {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return "", err
	}

	if !r.IsAdmin {
		canJoin := false
		for _, u := range room.Users {
			if u.Id == r.UserId {
				canJoin = true
			}
		}
		if !canJoin {
			return "", errors.New("you can't join in this room")
		}
	}

	p, meta, err := m.roomService.LoadParticipantWithMetadata(r.RoomId, r.UserId)
	if err != nil {
		return "", err
	}

	req := &plugnmeet.GenerateTokenReq{
		RoomId: r.BreakoutRoomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId:       r.UserId,
			Name:         p.Name,
			IsAdmin:      meta.IsAdmin,
			UserMetadata: meta,
		},
	}

	token, err := m.authTokenModel.DoGenerateToken(req)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *breakoutRoom) GetBreakoutRooms(roomId string) ([]*BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	return breakoutRooms, nil
}

func (m *breakoutRoom) GetMyBreakoutRooms(roomId, userId string) (*BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	for _, rr := range breakoutRooms {
		for _, u := range rr.Users {
			if u.Id == userId {
				return rr, nil
			}
		}
	}

	return nil, errors.New("not found")
}

type IncreaseBreakoutRoomDurationReq struct {
	RoomId         string
	BreakoutRoomId string `json:"breakout_room_id" validate:"required"`
	Duration       uint64 `json:"duration" validate:"required"`
}

func (m *breakoutRoom) IncreaseBreakoutRoomDuration(r *IncreaseBreakoutRoomDurationReq) error {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}

	// update in room duration checker
	req := new(RedisRoomDurationCheckerReq)
	req.Type = "increaseDuration"
	req.RoomId = r.BreakoutRoomId
	req.Duration = r.Duration
	reqMar, err := json.Marshal(req)
	if err != nil {
		return err
	}
	m.rc.Publish(m.ctx, "plug-n-meet-room-duration-checker", reqMar)

	// now update redis
	room.Duration += r.Duration
	marshal, err := json.Marshal(room)
	if err != nil {
		return err
	}
	val := map[string]string{
		r.BreakoutRoomId: string(marshal),
	}
	pp := m.rc.Pipeline()
	pp.HSet(m.ctx, breakoutRoomKey+r.RoomId, val)
	_, err = pp.Exec(m.ctx)

	return err
}

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *breakoutRoom) SendBreakoutRoomMsg(r *SendBreakoutRoomMsgReq) error {
	rooms, err := m.fetchBreakoutRooms(r.RoomId)
	if err != nil {
		return err
	}

	for _, rr := range rooms {
		err = m.broadcastNotification(rr.Id, "system", "", r.Msg, plugnmeet.DataMsgType_USER, plugnmeet.DataMsgBodyType_CHAT, true)
		if err != nil {
			continue
		}
	}

	return nil
}

type EndBreakoutRoomReq struct {
	RoomId         string
	BreakoutRoomId string `json:"breakout_room_id" validate:"required"`
}

func (m *breakoutRoom) EndBreakoutRoom(r *EndBreakoutRoomReq) error {
	_, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}
	_, err = m.roomService.EndRoom(r.BreakoutRoomId)
	if err != nil {
		log.Error(err)
	}

	// for safety we'll delete rooms
	_ = m.roomService.DeleteRoomFromRedis(r.BreakoutRoomId)
	model := NewRoomModel()
	_, _ = model.UpdateRoomStatus(&RoomInfo{
		RoomId:    r.BreakoutRoomId,
		IsRunning: 0,
		Ended:     time.Now().Format("2006-01-02 15:04:05"),
	})

	m.rc.HDel(m.ctx, breakoutRoomKey+r.RoomId, r.BreakoutRoomId)
	_ = m.performPostHookTask(r.RoomId)
	return nil
}

func (m *breakoutRoom) EndBreakoutRooms(roomId string) error {
	rooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return err
	}

	for _, r := range rooms {
		_ = m.EndBreakoutRoom(&EndBreakoutRoomReq{
			BreakoutRoomId: r.Id,
			RoomId:         roomId,
		})
	}
	return nil
}

func (m *breakoutRoom) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	if metadata.IsBreakoutRoom {
		room, err := m.fetchBreakoutRoom(metadata.ParentRoomId, roomId)
		if err != nil {
			return err
		}
		room.Created = int64(metadata.StartedAt)
		room.Started = true

		marshal, err := json.Marshal(room)
		if err != nil {
			return err
		}
		val := map[string]string{
			roomId: string(marshal),
		}
		pp := m.rc.Pipeline()
		pp.HSet(m.ctx, breakoutRoomKey+metadata.ParentRoomId, val)
		_, err = pp.Exec(m.ctx)
	}

	return nil
}

func (m *breakoutRoom) PostTaskAfterRoomEndWebhook(roomId, metadata string) error {
	if metadata == "" {
		return nil
	}
	meta := new(plugnmeet.RoomMetadata)
	err := json.Unmarshal([]byte(metadata), meta)
	if err != nil {
		return err
	}

	if meta.IsBreakoutRoom {
		m.rc.HDel(m.ctx, breakoutRoomKey+meta.ParentRoomId, roomId)
		_ = m.performPostHookTask(meta.ParentRoomId)
	} else {
		err = m.EndBreakoutRooms(roomId)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *breakoutRoom) broadcastNotification(roomId, fromUserId, toUserId, broadcastMsg string, typeMsg plugnmeet.DataMsgType, mType plugnmeet.DataMsgBodyType, isAdmin bool) error {
	payload := &plugnmeet.DataMessage{
		Type:   typeMsg,
		RoomId: roomId,
		Body: &plugnmeet.DataMsgBody{
			Type: mType,
			From: &plugnmeet.DataMsgReqFrom{
				UserId: fromUserId,
			},
			Msg: broadcastMsg,
		},
	}
	if toUserId != "" {
		payload.To = &toUserId
	}

	msg := &WebsocketToRedis{
		Type:    "sendMsg",
		DataMsg: payload,
		RoomId:  roomId,
		IsAdmin: isAdmin,
	}
	DistributeWebsocketMsgToRedisChannel(msg)

	return nil
}

func (m *breakoutRoom) fetchBreakoutRoom(roomId, breakoutRoomId string) (*BreakoutRoom, error) {
	cmd := m.rc.HGet(m.ctx, breakoutRoomKey+roomId, breakoutRoomId)
	result, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	if result == "" {
		return nil, errors.New("not found")
	}

	room := new(BreakoutRoom)
	err = json.Unmarshal([]byte(result), room)
	if err != nil {
		return nil, err
	}

	return room, nil
}

func (m *breakoutRoom) fetchBreakoutRooms(roomId string) ([]*BreakoutRoom, error) {
	cmd := m.rc.HGetAll(m.ctx, breakoutRoomKey+roomId)
	rooms, err := cmd.Result()
	if err != nil {
		return nil, err
	}
	if rooms == nil {
		return nil, errors.New("no breakout room found")
	}

	var breakoutRooms []*BreakoutRoom
	for i, r := range rooms {
		room := new(BreakoutRoom)
		err := json.Unmarshal([]byte(r), room)
		if err != nil {
			continue
		}
		room.Id = i
		for _, u := range room.Users {
			if room.Started {
				joined, err := m.roomService.LoadParticipantInfoFromRedis(room.Id, u.Id)
				if err == nil {
					if joined.Identity == u.Id {
						u.Joined = true
					}
				}
			}
		}
		breakoutRooms = append(breakoutRooms, room)
	}

	return breakoutRooms, nil
}

func (m *breakoutRoom) performPostHookTask(roomId string) error {
	cmd := m.rc.HLen(m.ctx, breakoutRoomKey+roomId)
	c, err := cmd.Result()
	if err != nil {
		log.Error(err)
		return err
	}

	if c != 0 {
		return nil
	}

	// no room left so, delete breakoutRoomKey key for this room
	m.rc.Del(m.ctx, breakoutRoomKey+roomId)

	// if no rooms left then we can update metadata
	_, meta, err := m.roomService.LoadRoomWithMetadata(roomId)
	if err != nil {
		return err
	}
	meta.RoomFeatures.BreakoutRoomFeatures.IsActive = false
	_, err = m.roomService.UpdateRoomMetadataByStruct(roomId, meta)
	if err != nil {
		return err
	}

	return nil
}
