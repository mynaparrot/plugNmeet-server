package controllers

import (
	"github.com/antoniodipinto/ikisocket"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/pkg/config"
	"github.com/mynaparrot/plugNmeet/pkg/models"
	log "github.com/sirupsen/logrus"
)

type websocketController struct {
	kws         *ikisocket.Websocket
	token       string
	participant config.ChatParticipant
}

func newWebsocketController(kws *ikisocket.Websocket) *websocketController {
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
	}
}

func (c *websocketController) validation() bool {
	m := models.NewAuthTokenModel()
	info := &models.ValidateTokenReq{
		Token: c.token,
	}

	claims, err := m.DoValidateToken(info, false)
	if err != nil {
		err = c.kws.EmitTo(c.kws.UUID, []byte("invalid token"))
		if err == nil {
			return false
		}
	}

	if claims.Identity != c.participant.UserId || claims.Video.Room != c.participant.RoomId {
		err = c.kws.EmitTo(c.kws.UUID, []byte("unauthorized access!"))
		if err == nil {
			return false
		}
	}

	c.participant.Name = claims.Name
	// default set false
	c.kws.SetAttribute("isAdmin", false)

	metadata := new(models.UserMetadata)
	err = json.Unmarshal([]byte(claims.Metadata), metadata)
	if err != nil {
		_ = c.kws.EmitTo(c.kws.UUID, []byte("can't Unmarshal metadata!"))
		return false
	}

	if metadata.IsAdmin {
		c.kws.SetAttribute("isAdmin", true)
		c.participant.IsAdmin = true
	}
	return true
}

func (c *websocketController) addUser() {
	config.AppCnf.AddChatUser(c.participant.RoomId, c.participant)
	c.kws.SetAttribute("userId", c.participant.UserId)
	c.kws.SetAttribute("roomId", c.participant.RoomId)
}

func HandleWebSocket() func(*fiber.Ctx) error {
	return ikisocket.New(func(kws *ikisocket.Websocket) {
		wc := newWebsocketController(kws)
		isValid := wc.validation()

		if isValid {
			wc.addUser()
		} else {
			kws.Close()
		}
	})
}

func SetupSocketListeners() {
	// On message event
	ikisocket.On(ikisocket.EventMessage, func(ep *ikisocket.EventPayload) {
		//fmt.Println(fmt.Sprintf("Message event - User: %s - Message: %s", ep.Kws.GetStringAttribute("userId"), string(ep.Data)))

		payload := &models.DataMessageRes{}
		err := json.Unmarshal(ep.Data, payload)
		if err != nil {
			log.Errorln(err)
			return
		}
		roomId := ep.Kws.GetStringAttribute("roomId")
		msg := models.WebsocketRedisMsg{
			Type:    "sendMsg",
			Payload: payload,
			RoomId:  roomId,
			IsAdmin: false,
		}
		isAdmin := ep.Kws.GetAttribute("isAdmin")
		if isAdmin != nil {
			msg.IsAdmin = isAdmin.(bool)
		}

		models.DistributeWebsocketMsgToRedisChannel(&msg)
	})

	// On disconnect event
	ikisocket.On(ikisocket.EventDisconnect, func(ep *ikisocket.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.AppCnf.RemoveChatParticipant(roomId, userId)
	})

	// This event is called when the server disconnects the user actively with .Close() method
	ikisocket.On(ikisocket.EventClose, func(ep *ikisocket.EventPayload) {
		roomId := ep.Kws.GetStringAttribute("roomId")
		userId := ep.Kws.GetStringAttribute("userId")
		// Remove the user from the local clients
		config.AppCnf.RemoveChatParticipant(roomId, userId)
	})

	// On error event
	//ikisocket.On(ikisocket.EventError, func(ep *ikisocket.EventPayload) {
	//	log.Errorln(fmt.Sprintf("Error event - User: %s", ep.Kws.GetStringAttribute("userSid")), ep.Error)
	//})
}

// SubscribeToWebsocketChannel will subscribe to all websocket channels
func SubscribeToWebsocketChannel() {
	go models.SubscribeToUserWebsocketChannel()
	go models.SubscribeToWhiteboardWebsocketChannel()
	go models.SubscribeToSystemWebsocketChannel()
}
