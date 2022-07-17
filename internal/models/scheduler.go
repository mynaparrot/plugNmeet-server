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
	ra          *roomAuthModel
	ticker      *time.Ticker
	closeTicker chan bool
}

func NewSchedulerModel() *scheduler {
	return &scheduler{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
		ra:  NewRoomAuthModel(),
	}
}

func (s *scheduler) StartScheduler() {
	go s.subscribeRedisRoomDurationChecker()
	go s.startActiveRoomChecker()

	s.ticker = time.NewTicker(5 * time.Second)
	defer s.ticker.Stop()
	s.closeTicker = make(chan bool)

	for {
		select {
		case <-s.closeTicker:
			return
		case <-s.ticker.C:
			s.checkRoomWithDuration()
		}
	}
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
			_, err := s.ra.rs.EndRoom(i)
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

// startActiveRoomChecker will check & do reconciliation between DB & livekit
func (s *scheduler) startActiveRoomChecker() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	closeTicker := make(chan bool)

	for {
		select {
		case <-closeTicker:
			return
		case <-ticker.C:
			s.activeRoomChecker()
		}
	}
}

func (s *scheduler) activeRoomChecker() {
	status, _, activeRooms := s.ra.GetActiveRoomsInfo()
	if !status {
		return
	}

	if len(activeRooms) == 0 {
		return
	}

	for _, room := range activeRooms {
		fromRedis, err := s.ra.rs.LoadRoomInfoFromRedis(room.RoomInfo.RoomId)

		if fromRedis == nil && err.Error() == "requested room does not exist" {
			_, _ = s.ra.rm.UpdateRoomStatus(&RoomInfo{
				Sid:       room.RoomInfo.Sid,
				IsRunning: 0,
				Ended:     time.Now().Format("2006-01-02 15:04:05"),
			})
			continue
		}

		if room.RoomInfo.JoinedParticipants != int64(fromRedis.NumParticipants) {
			_, _ = s.ra.rm.UpdateNumParticipants(room.RoomInfo.Sid, int64(fromRedis.NumParticipants))
		}
	}
}
