package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type scheduler struct {
	rc          *redis.Client
	ctx         context.Context
	roomService *RoomService
	ticker      *time.Ticker
	closeTicker chan bool
}

func NewScheduler() *scheduler {
	return &scheduler{
		rc:          config.AppCnf.RDS,
		ctx:         context.Background(),
		roomService: NewRoomService(),
	}
}

func (s *scheduler) StartScheduler() {
	go s.subscribeRedisRoomDurationChecker()

	s.ticker = time.NewTicker(5 * time.Second)
	s.closeTicker = make(chan bool)

	go func() {
		for {
			select {
			case <-s.closeTicker:
				return
			case <-s.ticker.C:
				s.checkRoomWithDuration()
			}
		}
	}()
}

func (s *scheduler) subscribeRedisRoomDurationChecker() {
	pubsub := s.rc.Subscribe(s.ctx, "plug-n-meet-room-duration-checker")
	defer pubsub.Close()

	_, err := pubsub.Receive(s.ctx)
	if err != nil {
		log.Fatalln(err)
	}
	ch := pubsub.Channel()
	for msg := range ch {
		config.AppCnf.DeleteRoomFromRoomWithDurationMap(msg.Payload)
	}
}

func (s *scheduler) checkRoomWithDuration() {
	config.AppCnf.RLock()
	rooms := config.AppCnf.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := time.Now().Unix()
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, err := s.roomService.EndRoom(i)
			if err != nil {
				log.Error(err)
			}
		}
	}
	config.AppCnf.RUnlock()
}
