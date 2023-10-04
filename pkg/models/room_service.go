package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const (
	BlockedUsersList = "pnm:block_users_list:"
	ActiveRoomsKey   = "pnm:activeRooms"
	ActiveRoomUsers  = "pnm:activeRoom:%s:users"
)

type RoomService struct {
	rc            *redis.Client
	ctx           context.Context
	livekitClient *lksdk.RoomServiceClient
}

func NewRoomService() *RoomService {
	livekitClient := lksdk.NewRoomServiceClient(config.AppCnf.LivekitInfo.Host, config.AppCnf.LivekitInfo.ApiKey, config.AppCnf.LivekitInfo.Secret)

	return &RoomService{
		rc:            config.AppCnf.RDS,
		ctx:           context.Background(),
		livekitClient: livekitClient,
	}
}

// LoadRoomInfo will room information from livekit
func (r *RoomService) LoadRoomInfo(roomId string) (*livekit.Room, error) {
	req := livekit.ListRoomsRequest{
		Names: []string{
			roomId,
		},
	}

	res, err := r.livekitClient.ListRooms(r.ctx, &req)
	if err != nil {
		return nil, err
	}

	if len(res.Rooms) == 0 {
		// if you change this text then make sure
		// you also update: scheduler.go activeRoomChecker()
		// also room_auth.go CreateRoom()
		return nil, errors.New("requested room does not exist")
	}

	room := res.Rooms[0]
	return room, nil
}

// LoadParticipants will load all the participants info from livekit
func (r *RoomService) LoadParticipants(roomId string) ([]*livekit.ParticipantInfo, error) {
	req := livekit.ListParticipantsRequest{
		Room: roomId,
	}
	res, err := r.livekitClient.ListParticipants(r.ctx, &req)
	if err != nil {
		return nil, err
	}
	return res.Participants, nil
}

// LoadParticipantInfo will load single participant info by identity
func (r *RoomService) LoadParticipantInfo(roomId string, identity string) (*livekit.ParticipantInfo, error) {
	req := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: identity,
	}

	participant, err := r.livekitClient.GetParticipant(r.ctx, &req)
	if err != nil {
		return nil, err
	}
	if participant == nil {
		return nil, errors.New("participant not found")
	}

	return participant, nil
}

// CreateRoom will create room in livekit
func (r *RoomService) CreateRoom(roomId string, emptyTimeout *uint32, maxParticipants *uint32, metadata string) (*livekit.Room, error) {
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

	room, err := r.livekitClient.CreateRoom(r.ctx, req)
	if err != nil {
		return nil, err
	}

	return room, nil
}

// UpdateRoomMetadata will directly send request to livekit to update metadata
func (r *RoomService) UpdateRoomMetadata(roomId string, metadata string) (*livekit.Room, error) {
	data := livekit.UpdateRoomMetadataRequest{
		Room:     roomId,
		Metadata: metadata,
	}

	room, err := r.livekitClient.UpdateRoomMetadata(r.ctx, &data)
	if err != nil {
		return nil, err
	}

	// temporary we'll update metadata manually
	// because livekit propagated quite lately now
	m := NewDataMessageModel()
	err = m.SendUpdatedMetadata(roomId, metadata)
	if err != nil {
		return nil, err
	}

	return room, nil
}

// EndRoom will send API request to livekit
func (r *RoomService) EndRoom(roomId string) (string, error) {
	data := livekit.DeleteRoomRequest{
		Room: roomId,
	}

	res, err := r.livekitClient.DeleteRoom(r.ctx, &data)
	if err != nil {
		return "", err
	}

	return res.String(), nil
}

// UpdateParticipantMetadata will directly send request to livekit to update metadata
func (r *RoomService) UpdateParticipantMetadata(roomId string, userId string, metadata string) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:     roomId,
		Identity: userId,
		Metadata: metadata,
	}

	participant, err := r.livekitClient.UpdateParticipant(r.ctx, &data)
	if err != nil {
		return nil, err
	}

	return participant, nil
}

// UpdateParticipantPermission will change user's permission by sending request to livekit
func (r *RoomService) UpdateParticipantPermission(roomId string, userId string, permission *livekit.ParticipantPermission) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:       roomId,
		Identity:   userId,
		Permission: permission,
	}

	participant, err := r.livekitClient.UpdateParticipant(r.ctx, &data)
	if err != nil {
		return nil, err
	}

	return participant, nil
}

// RemoveParticipant will send request to livekit to remove user
func (r *RoomService) RemoveParticipant(roomId string, userId string) (*livekit.RemoveParticipantResponse, error) {
	data := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: userId,
	}

	res, err := r.livekitClient.RemoveParticipant(r.ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}

// MuteUnMuteTrack can be used to mute/unmute track. This will send request to livekit
func (r *RoomService) MuteUnMuteTrack(roomId string, userId string, trackSid string, muted bool) (*livekit.MuteRoomTrackResponse, error) {
	data := livekit.MuteRoomTrackRequest{
		Room:     roomId,
		Identity: userId,
		TrackSid: trackSid,
		Muted:    muted,
	}

	res, err := r.livekitClient.MutePublishedTrack(r.ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}

// SendData will send request to livekit for sending data message
func (r *RoomService) SendData(roomId string, data []byte, dataPacket_Kind livekit.DataPacket_Kind, destinationSids []string) (*livekit.SendDataResponse, error) {
	req := livekit.SendDataRequest{
		Room:            roomId,
		Data:            data,
		Kind:            dataPacket_Kind,
		DestinationSids: destinationSids,
	}

	res, err := r.livekitClient.SendData(r.ctx, &req)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// AddUserToBlockList will add users to block list, we're using redis set
func (r *RoomService) AddUserToBlockList(roomId, userId string) (int64, error) {
	key := BlockedUsersList + roomId
	return r.rc.SAdd(r.ctx, key, userId).Result()
}

// IsUserExistInBlockList to check if user is present in the block list
func (r *RoomService) IsUserExistInBlockList(roomId, userId string) bool {
	key := BlockedUsersList + roomId
	exist, err := r.rc.SIsMember(r.ctx, key, userId).Result()
	if err != nil {
		return false
	}
	return exist
}

// DeleteRoomBlockList to completely delete block list set to provided roomId
func (r *RoomService) DeleteRoomBlockList(roomId string) (int64, error) {
	key := BlockedUsersList + roomId
	return r.rc.Del(r.ctx, key).Result()
}

// UnmarshalRoomMetadata will convert metadata string to proper format
func (r *RoomService) UnmarshalRoomMetadata(metadata string) (*plugnmeet.RoomMetadata, error) {
	meta := new(plugnmeet.RoomMetadata)
	err := protojson.Unmarshal([]byte(metadata), meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

// MarshalRoomMetadata will convert metadata struct to proper json format
func (r *RoomService) MarshalRoomMetadata(meta *plugnmeet.RoomMetadata) (string, error) {
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
func (r *RoomService) LoadRoomWithMetadata(roomId string) (*livekit.Room, *plugnmeet.RoomMetadata, error) {
	room, err := r.LoadRoomInfo(roomId)
	if err != nil {
		return nil, nil, err
	}

	if room.Metadata == "" {
		return room, nil, errors.New("empty metadata")
	}

	meta, err := r.UnmarshalRoomMetadata(room.Metadata)
	if err != nil {
		return room, nil, err
	}

	return room, meta, nil
}

// UpdateRoomMetadataByStruct to update metadata by providing formatted metadata
func (r *RoomService) UpdateRoomMetadataByStruct(roomId string, meta *plugnmeet.RoomMetadata) (*livekit.Room, error) {
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

// MarshalParticipantMetadata will create proper json string of user's metadata
func (r *RoomService) MarshalParticipantMetadata(meta *plugnmeet.UserMetadata) (string, error) {
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
func (r *RoomService) UnmarshalParticipantMetadata(metadata string) (*plugnmeet.UserMetadata, error) {
	m := new(plugnmeet.UserMetadata)
	err := protojson.Unmarshal([]byte(metadata), m)
	if err != nil {
		return nil, err
	}

	return m, nil
}

// LoadParticipantWithMetadata to load participant with proper formatted metadata
func (r *RoomService) LoadParticipantWithMetadata(roomId, userId string) (*livekit.ParticipantInfo, *plugnmeet.UserMetadata, error) {
	p, err := r.LoadParticipantInfo(roomId, userId)
	if err != nil {
		return nil, nil, err
	}

	meta, err := r.UnmarshalParticipantMetadata(p.Metadata)
	if err != nil {
		return p, nil, err
	}

	return p, meta, nil
}

// UpdateParticipantMetadataByStruct will update user's medata by provided formatted metadata
func (r *RoomService) UpdateParticipantMetadataByStruct(roomId, userId string, meta *plugnmeet.UserMetadata) (*livekit.ParticipantInfo, error) {
	metadata, err := r.MarshalParticipantMetadata(meta)
	if err != nil {
		return nil, err
	}
	p, err := r.UpdateParticipantMetadata(roomId, userId, metadata)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// ManageActiveRoomsList will use redis sorted sets to manage active sessions
// task = add | del | get | fetchAll
func (r *RoomService) ManageActiveRoomsList(roomId, task string, timeStamp int64) ([]redis.Z, error) {
	if timeStamp == 0 {
		timeStamp = time.Now().Unix()
	}
	var out []redis.Z
	var err error

	switch task {
	case "add":
		_, err = r.rc.ZAdd(r.ctx, ActiveRoomsKey, redis.Z{
			Score:  float64(timeStamp),
			Member: roomId,
		}).Result()
		if err != nil {
			return out, err
		}
	case "del":
		_, err = r.rc.ZRem(r.ctx, ActiveRoomsKey, roomId).Result()
		if err != nil {
			return out, err
		}
	case "get":
		result, err := r.rc.ZScore(r.ctx, ActiveRoomsKey, roomId).Result()
		switch {
		case err == redis.Nil:
			return out, err
		case err != nil:
			return out, err
		case result == 0:
			return out, nil
		}

		out = append(out, redis.Z{
			Member: roomId,
			Score:  result,
		})
	case "fetchAll":
		out, err = r.rc.ZRandMemberWithScores(r.ctx, ActiveRoomsKey, -1).Result()
		if err != nil {
			return out, err
		}
	}

	return out, nil
}

// ManageActiveUsersList will use redis sorted sets to manage active users
// task = add | del | get | fetchAll | delList (to delete this entire list)
func (r *RoomService) ManageActiveUsersList(roomId, userId, task string, timeStamp int64) ([]redis.Z, error) {
	if timeStamp == 0 {
		timeStamp = time.Now().Unix()
	}
	key := fmt.Sprintf(ActiveRoomUsers, roomId)
	var out []redis.Z
	var err error

	switch task {
	case "add":
		_, err = r.rc.ZAdd(r.ctx, key, redis.Z{
			Score:  float64(timeStamp),
			Member: userId,
		}).Result()
		if err != nil {
			return out, err
		}
	case "del":
		_, err = r.rc.ZRem(r.ctx, key, userId).Result()
		if err != nil {
			return out, err
		}
	case "delList":
		// this will delete this key completely
		// we'll trigger this when the session was ended
		_, err = r.rc.Del(r.ctx, key).Result()
		if err != nil {
			return out, err
		}
	case "get":
		result, err := r.rc.ZScore(r.ctx, key, userId).Result()
		switch {
		case err == redis.Nil:
			return out, err
		case err != nil:
			return out, err
		case result == 0:
			return out, nil
		}

		out = append(out, redis.Z{
			Member: userId,
			Score:  result,
		})
	case "fetchAll":
		out, err = r.rc.ZRandMemberWithScores(r.ctx, key, -1).Result()
		if err != nil {
			return out, err
		}
	}

	return out, nil
}
