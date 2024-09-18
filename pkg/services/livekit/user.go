package livekitservice

import (
	"errors"
	"github.com/livekit/protocol/livekit"
)

// LoadParticipants will load all the participant info from livekit
func (s *LivekitService) LoadParticipants(roomId string) ([]*livekit.ParticipantInfo, error) {
	req := livekit.ListParticipantsRequest{
		Room: roomId,
	}
	res, err := s.lkc.ListParticipants(s.ctx, &req)
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

	participant, err := s.lkc.GetParticipant(s.ctx, &req)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, errors.New("participant not found")
	}

	return participant, nil
}

// RemoveParticipant will send a request to livekit to remove user
func (s *LivekitService) RemoveParticipant(roomId string, userId string) (*livekit.RemoveParticipantResponse, error) {
	data := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: userId,
	}

	res, err := s.lkc.RemoveParticipant(s.ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}
