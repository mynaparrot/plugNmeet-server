package models

import (
	"context"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"
)

func DistributeWebsocketMsgToRedisChannel(payload *plugnmeet.WebsocketToRedis) {
	ctx := context.Background()
	msg, err := proto.Marshal(payload)
	if err != nil {
		log.Errorln(err)
		return
	}

	switch payload.DataMsg.Type {
	case plugnmeet.DataMsgType_USER:
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-user-websocket", msg)
	case plugnmeet.DataMsgType_WHITEBOARD:
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-whiteboard-websocket", msg)
	case plugnmeet.DataMsgType_SYSTEM:
		config.AppCnf.RDS.Publish(ctx, "plug-n-meet-system-websocket", msg)
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
		res := new(plugnmeet.WebsocketToRedis)
		err = proto.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		if res.Type == "sendMsg" {
			m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
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
		res := new(plugnmeet.WebsocketToRedis)
		err = proto.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
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
		res := new(plugnmeet.WebsocketToRedis)
		err = proto.Unmarshal([]byte(msg.Payload), res)
		if err != nil {
			log.Errorln(err)
		}
		m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
	}
}
