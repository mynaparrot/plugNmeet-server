package breakoutroommodel

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roommodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const breakoutRoomKey = "pnm:breakoutRoom:"

type BreakoutRoomModel struct {
	ds *dbservice.DatabaseService
	rs *redisservice.RedisService
	lk *livekitservice.LivekitService
	rm *roommodel.RoomModel
}

func NewBreakoutRoomModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *BreakoutRoomModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}
	if rs == nil {
		rs = redisservice.NewRedisService(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.NewLivekitService(rs)
	}

	return &BreakoutRoomModel{
		ds: ds,
		rs: rs,
		lk: lk,
		rm: roommodel.NewRoomModel(app, ds, rs, lk),
	}
}

func (m *BreakoutRoomModel) CreateBreakoutRooms(r *plugnmeet.CreateBreakoutRoomsReq) error {
	mainRoom, meta, err := m.lk.LoadRoomWithMetadata(r.RoomId)
	if err != nil {
		return err
	}

	// let's check if parent room has duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		rDuration := models.NewRoomDurationModel()
		err = rDuration.CompareDurationWithParentRoom(r.RoomId, r.Duration)
		if err != nil {
			return err
		}
	}

	// set room duration
	meta.RoomFeatures.RoomDuration = &r.Duration
	meta.IsBreakoutRoom = true
	meta.WelcomeMessage = r.WelcomeMsg
	meta.ParentRoomId = r.RoomId

	// disable few features
	meta.RoomFeatures.BreakoutRoomFeatures.IsAllow = false
	meta.RoomFeatures.WaitingRoomFeatures.IsActive = false

	// we'll disable now. in the future, we can think about those
	meta.RoomFeatures.RecordingFeatures.IsAllow = false
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
		status, msg, _ := m.rm.CreateRoom(bRoom)

		if !status {
			log.Error(msg)
			e[bRoom.RoomId] = true
			continue
		}

		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())

		marshal, err := protojson.Marshal(room)
		if err != nil {
			log.Error(err)
			e[bRoom.RoomId] = true
			continue
		}

		val := map[string]string{
			bRoom.RoomId: string(marshal),
		}

		err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, val)
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
	origMeta, err := m.lk.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		return err
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	_, err = m.lk.UpdateRoomMetadataByStruct(r.RoomId, origMeta)

	// send analytics
	analyticsModel := models.NewAnalyticsModel()
	analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	return err
}

func (m *BreakoutRoomModel) JoinBreakoutRoom(r *plugnmeet.JoinBreakoutRoomReq) (string, error) {
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

	p, meta, err := m.lk.LoadParticipantWithMetadata(r.RoomId, r.UserId)
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

	token, err := m.rm.GetPNMJoinToken(req)
	if err != nil {
		return "", err
	}

	return token, nil
}

func (m *BreakoutRoomModel) GetBreakoutRooms(roomId string) ([]*plugnmeet.BreakoutRoom, error) {
	breakoutRooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return nil, err
	}

	return breakoutRooms, nil
}

func (m *BreakoutRoomModel) GetMyBreakoutRooms(roomId, userId string) (*plugnmeet.BreakoutRoom, error) {
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

func (m *BreakoutRoomModel) IncreaseBreakoutRoomDuration(r *plugnmeet.IncreaseBreakoutRoomDurationReq) error {
	room, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}

	// update in room duration checker
	rd := models.NewRoomDurationModel()
	newDuration, err := rd.IncreaseRoomDuration(r.BreakoutRoomId, r.Duration)
	if err != nil {
		return err
	}

	// now update redis
	room.Duration = newDuration
	marshal, err := protojson.Marshal(room)
	if err != nil {
		return err
	}
	val := map[string]string{
		r.BreakoutRoomId: string(marshal),
	}
	err = m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, val)

	return err
}

type SendBreakoutRoomMsgReq struct {
	RoomId string
	Msg    string `json:"msg" validate:"required"`
}

func (m *BreakoutRoomModel) SendBreakoutRoomMsg(r *plugnmeet.BroadcastBreakoutRoomMsgReq) error {
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

func (m *BreakoutRoomModel) EndBreakoutRoom(r *plugnmeet.EndBreakoutRoomReq) error {
	_, err := m.fetchBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	if err != nil {
		return err
	}
	_, err = m.lk.EndRoom(r.BreakoutRoomId)
	if err != nil {
		log.Error(err)
	}

	_, _ = m.ds.UpdateRoomStatus(&dbmodels.RoomInfo{
		RoomId:    r.BreakoutRoomId,
		IsRunning: 0,
		Ended:     time.Now().UTC(),
	})

	_ = m.rs.DeleteBreakoutRoom(r.RoomId, r.BreakoutRoomId)
	_ = m.performPostHookTask(r.RoomId)
	return nil
}

func (m *BreakoutRoomModel) EndBreakoutRooms(roomId string) error {
	rooms, err := m.fetchBreakoutRooms(roomId)
	if err != nil {
		return err
	}

	for _, r := range rooms {
		_ = m.EndBreakoutRoom(&plugnmeet.EndBreakoutRoomReq{
			BreakoutRoomId: r.Id,
			RoomId:         roomId,
		})
	}
	return nil
}

func (m *BreakoutRoomModel) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	// now in livekit rooms are created almost instantly & sending webhook response
	// if this happened then we'll have to wait few seconds otherwise room info can't be found
	time.Sleep(config.WaitBeforeBreakoutRoomOnAfterRoomStart)

	room, err := m.fetchBreakoutRoom(metadata.ParentRoomId, roomId)
	if err != nil {
		return err
	}
	room.Created = metadata.StartedAt
	room.Started = true

	marshal, err := protojson.Marshal(room)
	if err != nil {
		return err
	}

	val := map[string]string{
		roomId: string(marshal),
	}
	err = m.rs.InsertOrUpdateBreakoutRoom(metadata.ParentRoomId, val)
	if err != nil {
		log.Error(err)
		return err
	}

	return nil
}

func (m *BreakoutRoomModel) PostTaskAfterRoomEndWebhook(roomId, metadata string) error {
	if metadata == "" {
		return nil
	}
	meta, err := m.lk.UnmarshalRoomMetadata(metadata)
	if err != nil {
		return err
	}

	if meta.IsBreakoutRoom {
		_ = m.rs.DeleteBreakoutRoom(meta.ParentRoomId, roomId)
		_ = m.performPostHookTask(meta.ParentRoomId)
	} else {
		err = m.EndBreakoutRooms(roomId)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *BreakoutRoomModel) broadcastNotification(roomId, fromUserId, toUserId, broadcastMsg string, typeMsg plugnmeet.DataMsgType, mType plugnmeet.DataMsgBodyType, isAdmin bool) error {
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

	msg := &models.WebsocketToRedis{
		Type:    "sendMsg",
		DataMsg: payload,
		RoomId:  roomId,
		IsAdmin: isAdmin,
	}
	models.DistributeWebsocketMsgToRedisChannel(msg)

	return nil
}

func (m *BreakoutRoomModel) fetchBreakoutRoom(roomId, breakoutRoomId string) (*plugnmeet.BreakoutRoom, error) {
	result, err := m.rs.GetBreakoutRoom(roomId, breakoutRoomId)
	if err != nil {
		return nil, err
	}
	if result == "" {
		return nil, errors.New("not info found")
	}

	room := new(plugnmeet.BreakoutRoom)
	err = protojson.Unmarshal([]byte(result), room)
	if err != nil {
		return nil, err
	}

	return room, nil
}

func (m *BreakoutRoomModel) fetchBreakoutRooms(roomId string) ([]*plugnmeet.BreakoutRoom, error) {
	rooms, err := m.rs.GetAllBreakoutRoomsByParentRoomId(roomId)
	if err != nil {
		return nil, err
	}
	if rooms == nil {
		return nil, errors.New("no breakout room found")
	}

	var breakoutRooms []*plugnmeet.BreakoutRoom
	for i, r := range rooms {
		room := new(plugnmeet.BreakoutRoom)
		err := protojson.Unmarshal([]byte(r), room)
		if err != nil {
			continue
		}
		room.Id = i
		for _, u := range room.Users {
			if room.Started {
				joined, err := m.rs.ManageActiveUsersList(room.Id, u.Id, "get", 0)
				if err == nil && len(joined) > 0 {
					u.Joined = true
				}
			}
		}
		breakoutRooms = append(breakoutRooms, room)
	}

	return breakoutRooms, nil
}

func (m *BreakoutRoomModel) performPostHookTask(roomId string) error {
	c, err := m.rs.CountBreakoutRooms(roomId)
	if err != nil {
		log.Error(err)
		return err
	}

	if c != 0 {
		return nil
	}

	// no room left so, delete breakoutRoomKey key for this room
	_ = m.rs.DeleteAllBreakoutRoomsByParentRoomId(roomId)

	// if no rooms left, then we can update metadata
	_, meta, err := m.lk.LoadRoomWithMetadata(roomId)
	if err != nil {
		return err
	}
	meta.RoomFeatures.BreakoutRoomFeatures.IsActive = false
	_, err = m.lk.UpdateRoomMetadataByStruct(roomId, meta)
	if err != nil {
		return err
	}

	return nil
}
