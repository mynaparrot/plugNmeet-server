package livekitservice

import (
	"errors"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
)

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

// UpdateRoomMetadataByStruct to update metadata by providing formatted metadata
func (r *LivekitService) UpdateRoomMetadataByStruct(roomId string, meta *plugnmeet.RoomMetadata) (*livekit.Room, error) {
	metadata, err := r.MarshalRoomMetadata(meta)
	if err != nil {
		return nil, err
	}
	room, err := r.UpdateRoomMetadata(roomId, metadata)
	if err != nil {
		return nil, err
	}

	return room, nil
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
