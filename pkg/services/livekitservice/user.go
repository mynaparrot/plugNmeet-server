package livekitservice

import (
	"errors"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
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

// LoadParticipantWithMetadata to load participant with proper formatted metadata
func (s *LivekitService) LoadParticipantWithMetadata(roomId, userId string) (*livekit.ParticipantInfo, *plugnmeet.UserMetadata, error) {
	p, err := s.LoadParticipantInfo(roomId, userId)
	if err != nil {
		return nil, nil, err
	}

	meta, err := s.UnmarshalParticipantMetadata(p.Metadata)
	if err != nil {
		return p, nil, err
	}

	return p, meta, nil
}

// UpdateParticipantMetadata will directly send request to livekit to update metadata
func (s *LivekitService) UpdateParticipantMetadata(roomId string, userId string, metadata string) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:     roomId,
		Identity: userId,
		Metadata: metadata,
	}

	participant, err := s.lkc.UpdateParticipant(s.ctx, &data)
	if err != nil {
		return nil, err
	}

	// we'll update our redis everytime
	_, err = s.rs.ManageRoomWithUsersMetadata(roomId, userId, "add", metadata)
	if err != nil {
		return nil, err
	}

	return participant, nil
}

// UpdateParticipantPermission will change user's permission by sending request to livekit
func (s *LivekitService) UpdateParticipantPermission(roomId string, userId string, permission *livekit.ParticipantPermission) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:       roomId,
		Identity:   userId,
		Permission: permission,
	}

	participant, err := s.lkc.UpdateParticipant(s.ctx, &data)
	if err != nil {
		return nil, err
	}

	return participant, nil
}

// RemoveParticipant will send request to livekit to remove user
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

// MarshalParticipantMetadata will create proper json string of user's metadata
func (s *LivekitService) MarshalParticipantMetadata(meta *plugnmeet.UserMetadata) (string, error) {
	mId := uuid.NewString()
	meta.MetadataId = &mId

	op := protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}
	marshal, err := op.Marshal(meta)
	if err != nil {
		return "", err
	}

	return string(marshal), nil
}

// UnmarshalParticipantMetadata will create proper formatted medata from json string
func (s *LivekitService) UnmarshalParticipantMetadata(metadata string) (*plugnmeet.UserMetadata, error) {
	m := new(plugnmeet.UserMetadata)
	err := protojson.Unmarshal([]byte(metadata), m)
	if err != nil {
		return nil, err
	}

	return m, nil
}
