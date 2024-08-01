package livekitservice

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
	"google.golang.org/protobuf/encoding/protojson"
)

type LivekitService struct {
	ctx context.Context
	lkc *lksdk.RoomServiceClient
	rs  *redisservice.RedisService
}

func NewLivekitService(rs *redisservice.RedisService) *LivekitService {
	cnf := config.GetConfig().LivekitInfo
	livekitClient := lksdk.NewRoomServiceClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	return &LivekitService{
		ctx: context.Background(),
		lkc: livekitClient,
		rs:  rs,
	}
}

// LoadRoomInfo will room information from livekit
func (s *LivekitService) LoadRoomInfo(roomId string) (*livekit.Room, error) {
	req := livekit.ListRoomsRequest{
		Names: []string{
			roomId,
		},
	}

	res, err := s.lkc.ListRooms(s.ctx, &req)
	if err != nil {
		return nil, err
	}

	if len(res.Rooms) == 0 {
		// if you change this text, then make sure
		// you also update: scheduler.go activeRoomChecker()
		// also room_auth.go CreateRoom()
		return nil, errors.New(config.RequestedRoomNotExist)
	}

	room := res.Rooms[0]
	return room, nil
}

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

// CreateRoom will create room in livekit
func (s *LivekitService) CreateRoom(roomId string, emptyTimeout *uint32, maxParticipants *uint32, metadata string) (*livekit.Room, error) {
	req := &livekit.CreateRoomRequest{
		Name: roomId,
	}
	if emptyTimeout != nil && *emptyTimeout > 0 {
		req.EmptyTimeout = *emptyTimeout
	}
	if maxParticipants != nil && *maxParticipants > 0 {
		req.MaxParticipants = *maxParticipants
	}
	if metadata != "" {
		req.Metadata = metadata
	}

	room, err := s.lkc.CreateRoom(s.ctx, req)
	if err != nil {
		return nil, err
	}

	return room, nil
}

// UpdateRoomMetadata will directly send request to livekit to update metadata
func (s *LivekitService) UpdateRoomMetadata(roomId string, metadata string) (*livekit.Room, error) {
	data := livekit.UpdateRoomMetadataRequest{
		Room:     roomId,
		Metadata: metadata,
	}

	room, err := s.lkc.UpdateRoomMetadata(s.ctx, &data)
	if err != nil {
		return nil, err
	}

	// we'll always update our own redis
	_, err = s.rs.ManageActiveRoomsWithMetadata(roomId, "add", metadata)
	if err != nil {
		return nil, err
	}

	// temporarily we'll update metadata manually
	// because livekit propagated quite lately now
	m := models.NewDataMessageModel()
	err = m.SendUpdatedMetadata(roomId, metadata)
	if err != nil {
		return nil, err
	}

	return room, nil
}

// EndRoom will send API request to livekit
func (s *LivekitService) EndRoom(roomId string) (string, error) {
	data := livekit.DeleteRoomRequest{
		Room: roomId,
	}

	res, err := s.lkc.DeleteRoom(s.ctx, &data)
	if err != nil {
		return "", err
	}

	return res.String(), nil
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

// MuteUnMuteTrack can be used to mute/unmute track. This will send request to livekit
func (s *LivekitService) MuteUnMuteTrack(roomId string, userId string, trackSid string, muted bool) (*livekit.MuteRoomTrackResponse, error) {
	data := livekit.MuteRoomTrackRequest{
		Room:     roomId,
		Identity: userId,
		TrackSid: trackSid,
		Muted:    muted,
	}

	res, err := s.lkc.MutePublishedTrack(s.ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}

// SendData will send a request to livekit for sending data message
func (s *LivekitService) SendData(roomId string, data []byte, dataPacketKind livekit.DataPacket_Kind, destinationUserIds []string) (*livekit.SendDataResponse, error) {
	req := livekit.SendDataRequest{
		Room:                  roomId,
		Data:                  data,
		Kind:                  dataPacketKind,
		DestinationIdentities: destinationUserIds,
	}

	res, err := s.lkc.SendData(s.ctx, &req)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// UnmarshalRoomMetadata will convert metadata string to proper format
func (s *LivekitService) UnmarshalRoomMetadata(metadata string) (*plugnmeet.RoomMetadata, error) {
	meta := new(plugnmeet.RoomMetadata)
	err := protojson.Unmarshal([]byte(metadata), meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

// MarshalRoomMetadata will convert metadata struct to proper json format
func (s *LivekitService) MarshalRoomMetadata(meta *plugnmeet.RoomMetadata) (string, error) {
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

// LoadRoomWithMetadata will load room info with proper formatted metadata
func (s *LivekitService) LoadRoomWithMetadata(roomId string) (*livekit.Room, *plugnmeet.RoomMetadata, error) {
	room, err := s.LoadRoomInfo(roomId)
	if err != nil {
		return nil, nil, err
	}

	if room.Metadata == "" {
		return room, nil, errors.New("empty metadata")
	}

	meta, err := s.UnmarshalRoomMetadata(room.Metadata)
	if err != nil {
		return room, nil, err
	}

	return room, meta, nil
}
