package models

import (
	"context"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
)

func DistributeWebsocketMsgToRedisChannel(msg *WebsocketRedisMsg) {
	ctx := context.Background()
	marshal, err := json.Marshal(msg)
	if err != nil {
		log.Errorln(err)
		return
	}

	switch msg.Payload.Type {
	case "USER":
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-user-websocket", marshal)
	case "WHITEBOARD":
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-whiteboard-websocket", marshal)
	case "SYSTEM":
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-system-websocket", marshal)
	}
}

// SubscribeToUserWebsocketChannel will delivery message to user websocket
func SubscribeToUserWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, "plug-n-meet-user-websocket")
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	ch := pubsub.Channel()
	for msg := range ch {
		res := new(WebsocketRedisMsg)
		err = json.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		if res.Type == "sendMsg" {
			m.HandleDataMessages(res.Payload, res.RoomId, res.IsAdmin)
		} else if res.Type == "deleteRoom" {
			config.AppCnf.DeleteChatRoom(res.RoomId)
		}
	}
}

// SubscribeToWhiteboardWebsocketChannel will delivery message to whiteboard websocket
func SubscribeToWhiteboardWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, "plug-n-meet-whiteboard-websocket")
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	ch := pubsub.Channel()
	for msg := range ch {
		res := new(WebsocketRedisMsg)
		err = json.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		m.HandleDataMessages(res.Payload, res.RoomId, res.IsAdmin)
	}
}

// SubscribeToSystemWebsocketChannel will delivery message to websocket
func SubscribeToSystemWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, "plug-n-meet-system-websocket")
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	ch := pubsub.Channel()
	for msg := range ch {
		res := new(WebsocketRedisMsg)
		err = json.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		m.HandleDataMessages(res.Payload, res.RoomId, res.IsAdmin)
	}
}
