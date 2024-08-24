package websocketmodel

import (
	"encoding/json"
	"fmt"
	"github.com/frostbyte73/core"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	log "github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

// SubscribeToUserWebsocketChannel will delivery message to user websocket
func (m *WebsocketModel) SubscribeToUserWebsocketChannel() {
	pubsub, err := m.rs.SubscribeToWebsocketChannel(config.UserWebsocketChannel)
	if err != nil {
		log.Fatalln(err)
	}
	defer pubsub.Close()

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
			res := new(redisservice.websocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			}
			if res != nil {
				if res.Type == "sendMsg" {
					m.HandleDataMessages(res.DataMsg, res.RoomId)
				} else if res.Type == "deleteRoom" {
					config.GetConfig().DeleteChatRoom(res.RoomId)
				}
			}
		})
	}
}

// SubscribeToWhiteboardWebsocketChannel will delivery message to whiteboard websocket
func (m *WebsocketModel) SubscribeToWhiteboardWebsocketChannel() {
	pubsub, err := m.rs.SubscribeToWebsocketChannel(config.WhiteboardWebsocketChannel)
	if err != nil {
		log.Fatalln(err)
	}
	defer pubsub.Close()

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
			res := new(redisservice.websocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			} else {
				m.HandleDataMessages(res.DataMsg, res.RoomId)
			}
		})
	}
}

// SubscribeToSystemWebsocketChannel will delivery message to websocket
func (m *WebsocketModel) SubscribeToSystemWebsocketChannel() {
	pubsub, err := m.rs.SubscribeToWebsocketChannel(config.SystemWebsocketChannel)
	if err != nil {
		log.Fatalln(err)
	}
	defer pubsub.Close()

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
			res := new(redisservice.websocketToRedis)
			err = json.Unmarshal([]byte(msg.Payload), res)
			if err != nil {
				log.Errorln(err)
			} else {
				m.HandleDataMessages(res.DataMsg, res.RoomId)
			}
		})
	}
}
