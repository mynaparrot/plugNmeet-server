package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type scheduler struct {
	rc          *redis.Client
	ctx         context.Context
	ra          *roomAuthModel
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

	s.closeTicker = make(chan bool)
	checkRoomDuration := time.NewTicker(5 * time.Second)
	defer checkRoomDuration.Stop()

	roomChecker := time.NewTicker(5 * time.Minute)
	defer roomChecker.Stop()

	for {
		select {
		case <-s.closeTicker:
			return
		case <-checkRoomDuration.C:
			s.checkRoomWithDuration()
		case <-roomChecker.C:
			s.activeRoomChecker()
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
			log.Errorln(err)
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
				log.Errorln(err)
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

// activeRoomChecker will check & do reconciliation between DB & livekit
func (s *scheduler) activeRoomChecker() {
	activeRooms, err := s.ra.rm.GetActiveRoomsInfo()
	if err != nil {
		return
	}

	if len(activeRooms) == 0 {
		return
	}

	for _, room := range activeRooms {
		fromRedis, err := s.ra.rs.LoadRoomInfoFromRedis(room.RoomId)

		if fromRedis == nil && err.Error() == "requested room does not exist" {
			_, _ = s.ra.rm.UpdateRoomStatus(&RoomInfo{
				Sid:       room.Sid,
				IsRunning: 0,
				Ended:     time.Now().Format("2006-01-02 15:04:05"),
			})
			continue
		} else if fromRedis == nil {
			continue
		}

		if room.JoinedParticipants != int64(fromRedis.NumParticipants) {
			_, _ = s.ra.rm.UpdateNumParticipants(room.Sid, int64(fromRedis.NumParticipants))
		}
	}
}
