package models

import (
	"context"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"time"
)

type SchedulerModel struct {
	rc          *redis.Client
	ctx         context.Context
	ra          *RoomAuthModel
	closeTicker chan bool
}

func NewSchedulerModel() *SchedulerModel {
	return &SchedulerModel{
		rc:  config.AppCnf.RDS,
		ctx: context.Background(),
		ra:  NewRoomAuthModel(),
	}
}

func (s *SchedulerModel) StartScheduler() {
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
	Duration uint64 `json:"duration"`
}

func (s *SchedulerModel) subscribeRedisRoomDurationChecker() {
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

func (s *SchedulerModel) checkRoomWithDuration() {
	config.AppCnf.RLock()
	defer config.AppCnf.RUnlock()

	rooms := config.AppCnf.GetRoomsWithDurationMap()
	for i, r := range rooms {
		now := uint64(time.Now().Unix())
		valid := r.StartedAt + (r.Duration * 60)
		if now > valid {
			_, err := s.ra.rs.EndRoom(i)
			if err != nil {
				log.Errorln(err)
			}
		}
	}
}

func (s *SchedulerModel) increaseRoomDuration(roomId string, duration uint64) {
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

	meta.RoomFeatures.RoomDuration = &newDuration
	_, err = roomService.UpdateRoomMetadataByStruct(roomId, meta)

	if err != nil {
		return
	}
}

// activeRoomChecker will check & do reconciliation between DB & livekit
func (s *SchedulerModel) activeRoomChecker() {
	activeRooms, err := s.ra.rm.GetActiveRoomsInfo()
	if err != nil {
		return
	}

	if len(activeRooms) == 0 {
		return
	}

	for _, room := range activeRooms {
		fromRedis, err := s.ra.rs.LoadRoomInfo(room.RoomId)

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

		pp, err := s.ra.rs.LoadParticipants(room.RoomId)
		if err != nil {
			continue
		}
		var count int64 = 0
		for _, p := range pp {
			if p.Identity == config.RECORDER_BOT || p.Identity == config.RTMP_BOT {
				continue
			}
			count++
		}
		if room.JoinedParticipants != count {
			_, _ = s.ra.rm.UpdateNumParticipants(room.Sid, count)
		} else if room.JoinedParticipants == 0 {
			// this room doesn't have any user
			// we'll check if room was created long before then we can end it
			// here we can check if room was created more than 24 hours ago
			expire := time.Unix(room.CreationTime, 0).Add(time.Hour * 24)
			if time.Now().After(expire) {
				// we can close the room
				s.ra.EndRoom(&plugnmeet.RoomEndReq{
					RoomId: room.RoomId,
				})
			}
		}
	}
}
