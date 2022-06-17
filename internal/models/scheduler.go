package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
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

func NewSchedulerModel() *scheduler {
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

type RedisRoomDurationCheckerReq struct {
	Type     string `json:"type"`
	RoomId   string `json:"room_id"`
	Duration int64  `json:"duration"`
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
		req := new(RedisRoomDurationCheckerReq)
		err := json.Unmarshal([]byte(msg.Payload), req)
		if err != nil {
			continue
		}
		if req.Type == "delete" {
			config.AppCnf.DeleteRoomFromRoomWithDurationMap(req.RoomId)
		} else if req.Type == "increaseDuration" {
			s.increaseRoomDuration(req.RoomId, req.Duration)
		}
	}
}

func (s *scheduler) checkRoomWithDuration() {
	config.AppCnf.RLock()
	defer config.AppCnf.RUnlock()

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
}

func (s *scheduler) increaseRoomDuration(roomId string, duration int64) {
	newDuration := config.AppCnf.IncreaseRoomDuration(roomId, duration)
	if newDuration == 0 {
		// so record not found in this server
		return
	}

	// increase room duration
	roomService := NewRoomService()
	_, meta, err := roomService.LoadRoomWithMetadata(roomId)
	if err != nil {
		return
	}

	meta.Features.RoomDuration = newDuration
	_, err = roomService.UpdateRoomMetadataByStruct(roomId, meta)

	if err != nil {
		return
	}
}
