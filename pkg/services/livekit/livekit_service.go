package livekitservice

import (
	"context"
	"fmt"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

type LivekitService struct {
	app    *config.AppConfig
	ctx    context.Context
	lkc    *lksdk.RoomServiceClient
	logger *logrus.Entry
}

func New(ctx context.Context, app *config.AppConfig, logger *logrus.Logger) *LivekitService {
	lkc := lksdk.NewRoomServiceClient(app.LivekitInfo.Host, app.LivekitInfo.ApiKey, app.LivekitInfo.Secret)

	return &LivekitService{
		ctx:    ctx,
		app:    app,
		lkc:    lkc,
		logger: logger.WithField("service", "livekit"),
	}
}

// EndRoom will send API request to livekit
func (s *LivekitService) EndRoom(roomId string) (string, error) {
	data := livekit.DeleteRoomRequest{
		Room: roomId,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*15)
	defer cancel()

	res, err := s.lkc.DeleteRoom(ctx, &data)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "no response received", nil
	}

	return res.String(), nil
}

// MuteUnMuteTrack can be used to mute/unmute track. This will send request to livekit
func (s *LivekitService) MuteUnMuteTrack(roomId string, userId string, trackSid string, muted bool) (*livekit.MuteRoomTrackResponse, error) {
	data := livekit.MuteRoomTrackRequest{
		Room:     roomId,
		Identity: userId,
		TrackSid: trackSid,
		Muted:    muted,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*10)
	defer cancel()

	res, err := s.lkc.MutePublishedTrack(ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}

// LoadParticipants will load all the participant info from livekit
func (s *LivekitService) LoadParticipants(roomId string) ([]*livekit.ParticipantInfo, error) {
	req := livekit.ListParticipantsRequest{
		Room: roomId,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*15)
	defer cancel()

	res, err := s.lkc.ListParticipants(ctx, &req)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	return res.Participants, nil
}

// LoadParticipantInfo will load single participant info by identity
func (s *LivekitService) LoadParticipantInfo(roomId string, identity string) (*livekit.ParticipantInfo, error) {
	req := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: identity,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*10)
	defer cancel()

	participant, err := s.lkc.GetParticipant(ctx, &req)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, fmt.Errorf("participant not found")
	}

	return participant, nil
}

// RemoveParticipant will send a request to livekit to remove user
func (s *LivekitService) RemoveParticipant(roomId string, userId string) (*livekit.RemoveParticipantResponse, error) {
	data := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: userId,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*10)
	defer cancel()

	res, err := s.lkc.RemoveParticipant(ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}

func (s *LivekitService) CreateIngress(req *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	cnf := s.app.LivekitInfo
	ic := lksdk.NewIngressClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	ctx, cancel := context.WithTimeout(s.ctx, time.Second*15)
	defer cancel()

	return ic.CreateIngress(ctx, req)
}
