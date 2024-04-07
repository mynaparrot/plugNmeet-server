package models

import (
	"context"
	"fmt"
	"github.com/frostbyte73/core"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

type WebsocketToRedis struct {
	Type    string                 `json:"type,omitempty"`
	DataMsg *plugnmeet.DataMessage `json:"data_msg,omitempty"`
	RoomId  string                 `json:"room_id,omitempty"`
	IsAdmin bool                   `json:"is_admin,omitempty"`
}

func DistributeWebsocketMsgToRedisChannel(payload *WebsocketToRedis) {
	ctx := context.Background()
	msg, err := json.Marshal(payload)
	if err != nil {
		log.Errorln(err)
		return
	}

	switch payload.DataMsg.Type {
	case plugnmeet.DataMsgType_USER:
		config.AppCnf.RDS.Publish(ctx, config.UserWebsocketChannel, msg)
	case plugnmeet.DataMsgType_WHITEBOARD:
		config.AppCnf.RDS.Publish(ctx, config.WhiteboardWebsocketChannel, msg)
	case plugnmeet.DataMsgType_SYSTEM:
		config.AppCnf.RDS.Publish(ctx, config.SystemWebsocketChannel, msg)
	}
}

// SubscribeToUserWebsocketChannel will delivery message to user websocket
func SubscribeToUserWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, config.UserWebsocketChannel)
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	var dropped atomic.Int32
	worker := core.NewQueueWorker(core.QueueWorkerParams{
		QueueSize:    config.DefaultWebsocketQueueSize,
		DropWhenFull: true,
		OnDropped: func() {
			l := dropped.Inc()
			log.Println(fmt.Sprintf("Total dropped user-websocket events: %d", l))
		},
	})

	ch := pubsub.Channel()
	for msg := range ch {
		worker.Submit(func() {
			res := new(WebsocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			}
			if res.Type == "sendMsg" {
				m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
			} else if res.Type == "deleteRoom" {
				config.AppCnf.DeleteChatRoom(res.RoomId)
			}
		})
	}
}

// SubscribeToWhiteboardWebsocketChannel will delivery message to whiteboard websocket
func SubscribeToWhiteboardWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, config.WhiteboardWebsocketChannel)
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	var dropped atomic.Int32
	worker := core.NewQueueWorker(core.QueueWorkerParams{
		QueueSize:    config.DefaultWebsocketQueueSize,
		DropWhenFull: true,
		OnDropped: func() {
			l := dropped.Inc()
			log.Println(fmt.Sprintf("Total dropped whiteboard-websocket data: %d", l))
		},
	})
	ch := pubsub.Channel()
	for msg := range ch {
		worker.Submit(func() {
			res := new(WebsocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			}
			m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
		})
	}
}

// SubscribeToSystemWebsocketChannel will delivery message to websocket
func SubscribeToSystemWebsocketChannel() {
	ctx := context.Background()
	pubsub := config.AppCnf.RDS.Subscribe(ctx, config.SystemWebsocketChannel)
	defer pubsub.Close()

	_, err := pubsub.Receive(ctx)
	if err != nil {
		log.Fatalln(err)
	}

	m := NewWebsocketService()
	var dropped atomic.Int32
	worker := core.NewQueueWorker(core.QueueWorkerParams{
		QueueSize:    config.DefaultWebsocketQueueSize,
		DropWhenFull: true,
		OnDropped: func() {
			l := dropped.Inc()
			log.Println(fmt.Sprintf("Total dropped system-websocket events: %d", l))
		},
	})

	ch := pubsub.Channel()
	for msg := range ch {
		worker.Submit(func() {
			res := new(WebsocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			}
			m.HandleDataMessages(res.DataMsg, res.RoomId, res.IsAdmin)
		})
	}
}
