package models

import (
	"context"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	turnservice "github.com/mynaparrot/plugnmeet-server/pkg/services/turn"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/encoding/protojson"
)

type NatsModel struct {
	ctx            context.Context
	app            *config.AppConfig
	ds             *dbservice.DatabaseService
	rs             *redisservice.RedisService
	lk             *livekitservice.LivekitService
	turn           *turnservice.TurnService
	authModel      *AuthModel
	natsService    *natsservice.NatsService
	userModel      *UserModel
	analyticsModel *AnalyticsModel
	aiAgentModel   *AIAgentModel
	logger         *logrus.Entry
	sfGroup        *singleflight.Group
}

type NatsModelArgs struct {
	fx.In
	Ctx            context.Context
	App            *config.AppConfig
	Ds             *dbservice.DatabaseService
	Rs             *redisservice.RedisService
	NatsService    *natsservice.NatsService
	Lk             *livekitservice.LivekitService
	Turn           *turnservice.TurnService
	AnalyticsModel *AnalyticsModel
	AuthModel      *AuthModel
	UserModel      *UserModel
	AIAgentModel   *AIAgentModel
	Logger         *logrus.Logger
}

func NewNatsModel(args NatsModelArgs) *NatsModel {
	return &NatsModel{
		ctx:            args.Ctx,
		app:            args.App,
		ds:             args.Ds,
		rs:             args.Rs,
		lk:             args.Lk,
		turn:           args.Turn,
		natsService:    args.NatsService,
		authModel:      args.AuthModel,
		userModel:      args.UserModel,
		analyticsModel: args.AnalyticsModel,
		aiAgentModel:   args.AIAgentModel,
		logger:         args.Logger.WithField("model", "nats"),
		sfGroup:        new(singleflight.Group),
	}
}

func (m *NatsModel) HandleFromClientToServerReq(roomId, userId string, req *plugnmeet.NatsMsgClientToServer) {
	if m.natsService.IsUserExistInBlockList(roomId, userId) {
		return
	}

	status, err := m.natsService.GetRoomUserStatus(roomId, userId)
	if err != nil || status == "" || status == natsservice.UserStatusRemoved {
		return
	}

	meta, err := m.natsService.GetUserMetadataStruct(roomId, userId)
	if err != nil || meta == nil {
		return
	}
	// a user waiting for approval is only allowed to do the bare minimum
	if meta.GetWaitForApproval() {
		switch req.Event {
		case plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA,
			plugnmeet.NatsMsgClientToServerEvents_REQ_RENEW_PNM_TOKEN,
			plugnmeet.NatsMsgClientToServerEvents_REQ_LEAVE_ROOM,
			plugnmeet.NatsMsgClientToServerEvents_PING:
			// allowed while waiting for approval
		default:
			return
		}
	}

	switch req.Event {
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RENEW_PNM_TOKEN:
		m.RenewPNMToken(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_INITIAL_DATA:
		m.HandleInitialData(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_JOINED_USERS_LIST:
		m.HandleSendUsersList(roomId, userId, nil)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_MEDIA_SERVER_DATA:
		m.HandleMediaServerInfo(roomId, userId, true)
	case plugnmeet.NatsMsgClientToServerEvents_PING:
		m.HandleClientPing(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LEAVE_ROOM:
		m.HandleUserLeaveRoom(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_RAISE_HAND:
		m.userModel.RaisedHand(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_HAND:
		m.userModel.LowerHand(roomId, userId)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_LOWER_OTHER_USER_HAND:
		m.userModel.LowerOtherUserHand(roomId, userId, req.Msg)
	case plugnmeet.NatsMsgClientToServerEvents_PUSH_ANALYTICS_DATA:
		ad := new(plugnmeet.AnalyticsDataMsg)
		err := protojson.Unmarshal([]byte(req.Msg), ad)
		if err != nil {
			m.logger.Errorln(err)
			return
		}
		isUserEvent := ad.EventType == plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_USER && ad.EventName != plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_UNKNOWN
		isAllowedRoomEvent := ad.EventType == plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM && ad.EventName == plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_WHITEBOARD_FILES
		if !isUserEvent && !isAllowedRoomEvent {
			return
		}
		// bind to the authenticated identity
		ad.RoomId = roomId
		ad.UserId = &userId
		m.analyticsModel.HandleEvent(ad)
	case plugnmeet.NatsMsgClientToServerEvents_REQ_ONLINE_USERS_LIST:
		m.HandleSendUsersList(roomId, userId, new(plugnmeet.NatsMsgServerToClientEvents_RESP_ONLINE_USERS_LIST))
	case plugnmeet.NatsMsgClientToServerEvents_REQ_PRIVATE_DATA_DELIVERY:
		m.HandleToDeliveryPrivateData(roomId, userId, req)
	case plugnmeet.NatsMsgClientToServerEvents_SESSION_DATA_SAVE:
		m.HandleSessionDataSave(roomId, userId, req)
	case plugnmeet.NatsMsgClientToServerEvents_SESSION_DATA_FETCH_REQUEST:
		m.HandleSessionDataFetchRequest(roomId, userId, req)
	case plugnmeet.NatsMsgClientToServerEvents_AI_AGENT_TOOL_RESULT:
		if m.aiAgentModel == nil {
			return
		}
		res := new(plugnmeet.AIAgentToolResultMsg)
		if err := protojson.Unmarshal([]byte(req.Msg), res); err != nil {
			m.logger.WithError(err).Warnln("failed to decode the ai agent tool result")
			return
		}
		m.aiAgentModel.HandleClientToolResult(m.ctx, roomId, userId, res)
	case plugnmeet.NatsMsgClientToServerEvents_AI_AGENT_WHITEBOARD_SCENE:
		if m.aiAgentModel == nil {
			return
		}
		scene := new(plugnmeet.AIAgentWhiteboardSceneMsg)
		if err := protojson.Unmarshal([]byte(req.Msg), scene); err != nil {
			m.logger.WithError(err).Warnln("failed to decode the ai agent whiteboard scene")
			return
		}
		// The image may travel in the binary payload to avoid base64 inflation.
		if len(req.BinMsg) > 0 && len(scene.WebpImage) == 0 {
			scene.WebpImage = req.BinMsg
		}
		m.aiAgentModel.HandleWhiteboardScene(m.ctx, roomId, userId, scene)
	}
}
