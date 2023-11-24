package models

import (
	"context"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"time"
)

type SchedulerModel struct {
	rc          *redis.Client
	ctx         context.Context
	ra          *RoomAuthModel
	rmDuration  *RoomDurationModel
	closeTicker chan bool
}

func NewSchedulerModel() *SchedulerModel {
	return &SchedulerModel{
		rc:         config.AppCnf.RDS,
		ctx:        context.Background(),
		ra:         NewRoomAuthModel(),
		rmDuration: NewRoomDurationModel(),
	}
}

func (s *SchedulerModel) StartScheduler() {
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

func (s *SchedulerModel) checkRoomWithDuration() {
	rooms := s.rmDuration.GetRoomsWithDurationMap()
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
		if room.Sid == "" {
			// if room Sid is empty then we won't do anything
			// because may be the session is creating
			// if we don't consider this then it will unnecessarily create empty field
			continue
		}

		fromRedis, err := s.ra.rs.LoadRoomInfo(room.RoomId)
		if fromRedis == nil && err.Error() == "requested room does not exist" {
			_, _ = s.ra.rm.UpdateRoomStatus(&RoomInfo{
				Sid:       room.Sid,
				IsRunning: 0,
				Ended:     time.Now().UTC().Format("2006-01-02 15:04:05"),
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
			if p.Identity == config.RecorderBot || p.Identity == config.RtmpBot {
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
			if time.Now().UTC().After(expire) {
				// we can close the room
				s.ra.EndRoom(&plugnmeet.RoomEndReq{
					RoomId: room.RoomId,
				})
			}
		}
	}
}
