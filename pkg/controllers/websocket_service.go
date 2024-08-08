package controllers

import (
	"github.com/gofiber/contrib/socketio"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/analyticsmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/roommodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/websocketmodel"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type websocketController struct {
	kws         *socketio.Websocket
	token       string
	participant config.ChatParticipant
	rm          *roommodel.RoomModel
}

func newWebsocketController(kws *socketio.Websocket) *websocketController {
	authToken := kws.Query("token")
	roomSid := kws.Query("roomSid")
	userSid := kws.Query("userSid")
	roomId := kws.Query("roomId")
	userId := kws.Query("userId")

	p := config.ChatParticipant{
		RoomSid: roomSid,
		RoomId:  roomId,
		UserSid: userSid,
		UserId:  userId,
		UUID:    kws.UUID,
	}

	return &websocketController{
		kws:         kws,
		participant: p,
		token:       authToken,
		rm:          roommodel.New(nil, nil, nil, nil),
	}
}

func (c *websocketController) validation() bool {
	if c.token == "" {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("empty auth token"), socketio.TextMessage)
		return false
	}

	claims, err := c.rm.VerifyPlugNmeetAccessToken(c.token)
	if err != nil {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("invalid auth token"), socketio.TextMessage)
		return false
	}

	if claims.UserId != c.participant.UserId || claims.RoomId != c.participant.RoomId {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("unauthorized access!"), socketio.TextMessage)
		return false
	}

	c.participant.Name = claims.Name
	// default set false
	c.kws.SetAttribute("isAdmin", claims.IsAdmin)
	c.participant.IsAdmin = claims.IsAdmin

	return true
}

func (c *websocketController) addUser() {
	config.GetConfig().AddChatUser(c.participant.RoomId, c.participant)
	c.kws.SetAttribute("userId", c.participant.UserId)
	c.kws.SetAttribute("roomId", c.participant.RoomId)
}

func HandleWebSocket(cnf websocket.Config) func(*fiber.Ctx) error {
	return socketio.New(func(kws *socketio.Websocket) {
		wc := newWebsocketController(kws)
		isValid := wc.validation()

		if isValid {
			wc.addUser()
		} else {
			if kws.IsAlive() {
				kws.Close()
			}
		}
	}, cnf)
}

func SetupSocketListeners() {
	app := config.GetConfig()
	rs := redisservice.NewRedisService(app.RDS)
	analytics := analyticsmodel.New(app, nil, rs, nil)

	// On message event
	socketio.On(socketio.EventMessage, func(ep *socketio.EventPayload) {
		//fmt.Println(fmt.Sprintf("Message event - User: %s - Message: %s", ep.Kws.GetStringAttribute("userId"), string(ep.Data)))
		dataMsg := &plugnmeet.DataMessage{}
		err := proto.Unmarshal(ep.Data, dataMsg)
		if err != nil {
			return
		}

		// for analytics type data we won't need to deliver anywhere
		if dataMsg.Body.Type == plugnmeet.DataMsgBodyType_ANALYTICS_DATA {
			ad := new(plugnmeet.AnalyticsDataMsg)
			err = protojson.Unmarshal([]byte(dataMsg.Body.Msg), ad)
			if err != nil {
				return
			}
			analytics.HandleEvent(ad)
			return
		}

		roomId := ep.Kws.GetStringAttribute("roomId")
		payload := &redisservice.WebsocketToRedis{
			Type:    "sendMsg",
			DataMsg: dataMsg,
			RoomId:  roomId,
			IsAdmin: false,
		}
		isAdmin := ep.Kws.GetAttribute("isAdmin")
		if isAdmin != nil {
			payload.IsAdmin = isAdmin.(bool)
		}

		rs.DistributeWebsocketMsgToRedisChannel(payload)
		// send analytics
		if dataMsg.Body.From != nil && dataMsg.Body.From.UserId != "" {
			analytics.HandleWebSocketData(dataMsg)
		}

	})

	// On disconnect event
	socketio.On(socketio.EventDisconnect, func(ep *socketio.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.GetConfig().RemoveChatParticipant(roomId, userId)
	})

	// This event is called when the server disconnects the user actively with .Close() method
	socketio.On(socketio.EventClose, func(ep *socketio.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.GetConfig().RemoveChatParticipant(roomId, userId)
	})

	// On error event
	//socketio.On(socketio.EventError, func(ep *socketio.EventPayload) {
	//	log.Errorln(fmt.Sprintf("Error event - User: %s", ep.Kws.GetStringAttribute("userSid")), ep.Error)
	//})
}

// SubscribeToWebsocketChannel will subscribe to all websocket channels
func SubscribeToWebsocketChannel() {
	m := websocketmodel.New(nil, nil, nil, nil)
	go m.SubscribeToUserWebsocketChannel()
	go m.SubscribeToWhiteboardWebsocketChannel()
	go m.SubscribeToSystemWebsocketChannel()
}
