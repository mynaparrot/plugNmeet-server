package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"time"
)

const (
	BlockedUsersList           = "pnm:block_users_list:"
	ActiveRoomsWithMetadataKey = "pnm:activeRoomsWithMetadata"
	ActiveRoomUsers            = "pnm:activeRoom:%s:users"
	RoomWithUsersMetadata      = "pnm:roomWithUsersMetadata:%s"
	RoomCreationProgressList   = "pnm:roomCreationProgressList"
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

	// we'll always update our own redis
	_, err = r.ManageActiveRoomsWithMetadata(roomId, "add", metadata)
	if err != nil {
		return nil, err
	}

	// temporarily we'll update metadata manually
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

	// we'll update our redis everytime
	_, err = r.ManageRoomWithUsersMetadata(roomId, userId, "add", metadata)
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

// SendData will send a request to livekit for sending data message
func (r *RoomService) SendData(roomId string, data []byte, dataPacketKind livekit.DataPacket_Kind, destinationUserIds []string) (*livekit.SendDataResponse, error) {
	req := livekit.SendDataRequest{
		Room:                  roomId,
		Data:                  data,
		Kind:                  dataPacketKind,
		DestinationIdentities: destinationUserIds,
	}

	res, err := r.livekitClient.SendData(r.ctx, &req)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// AddUserToBlockList will add users to blocklist, we're using redis set
func (r *RoomService) AddUserToBlockList(roomId, userId string) (int64, error) {
	key := BlockedUsersList + roomId
	return r.rc.SAdd(r.ctx, key, userId).Result()
}

// IsUserExistInBlockList to check if the user is present in the blocklist
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

// LoadOnlyRoomMetadata will load metadata from our own redis system
// we always update our redis metadata when we trigger to update.
func (r *RoomService) LoadOnlyRoomMetadata(roomId string) (*plugnmeet.RoomMetadata, error) {
	metadata, err := r.ManageActiveRoomsWithMetadata(roomId, "get", "")
	if err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, errors.New("empty metadata")
	}
	return r.UnmarshalRoomMetadata(metadata[roomId])
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

// LoadOnlyParticipantMetadata will load participant metadata from our managed redis with online status
func (r *RoomService) LoadOnlyParticipantMetadata(roomId, userId string) (*plugnmeet.UserMetadata, bool, error) {
	metadata, err := r.ManageRoomWithUsersMetadata(roomId, userId, "get", "")
	if err != nil {
		return nil, false, err
	}
	if metadata == "" {
		return nil, false, errors.New("empty metadata")
	}

	list, err := r.ManageActiveUsersList(roomId, userId, "get", 0)
	if err != nil {
		return nil, false, err
	}

	pm, err := r.UnmarshalParticipantMetadata(metadata)
	if err != nil {
		return nil, false, err
	}

	isOnline := false
	if len(list) > 0 {
		isOnline = true
	}

	return pm, isOnline, nil
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

// ManageActiveRoomsWithMetadata will use redis sorted active rooms with their metadata
// task = add | del | get | fetchAll
func (r *RoomService) ManageActiveRoomsWithMetadata(roomId, task, metadata string) (map[string]string, error) {
	var out map[string]string
	var err error

	switch task {
	case "add":
		_, err = r.rc.HSet(r.ctx, ActiveRoomsWithMetadataKey, roomId, metadata).Result()
		if err != nil {
			return nil, err
		}
	case "del":
		_, err = r.rc.HDel(r.ctx, ActiveRoomsWithMetadataKey, roomId).Result()
		if err != nil {
			return nil, err
		}
	case "get":
		result, err := r.rc.HGet(r.ctx, ActiveRoomsWithMetadataKey, roomId).Result()
		switch {
		case errors.Is(err, redis.Nil):
			return nil, nil
		case err != nil:
			return nil, err
		}
		out = map[string]string{
			roomId: result,
		}
	case "fetchAll":
		out, err = r.rc.HGetAll(r.ctx, ActiveRoomsWithMetadataKey).Result()
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

// ManageRoomWithUsersMetadata can be used to store user metadata in redis
// this way we'll be able to access info quickly
// task = add | del | get | delList (to delete this entire list)
func (r *RoomService) ManageRoomWithUsersMetadata(roomId, userId, task, metadata string) (string, error) {
	key := fmt.Sprintf(RoomWithUsersMetadata, roomId)
	switch task {
	case "add":
		_, err := r.rc.HSet(r.ctx, key, userId, metadata).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	case "get":
		result, err := r.rc.HGet(r.ctx, key, userId).Result()
		switch {
		case err == redis.Nil:
			return "", nil
		case err != nil:
			return "", err
		}
		return result, nil
	case "del":
		_, err := r.rc.HDel(r.ctx, key, userId).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	case "delList":
		// this will delete this key completely
		// we'll trigger this when the session was ended
		_, err := r.rc.Del(r.ctx, key).Result()
		if err != nil {
			return "", err
		}
		return "", nil
	}
	return "", errors.New("invalid task")
}

// RoomCreationProgressList can be used during a room creation
// we have seen that during create room in livekit an instant webhook sent from livekit but from our side we are still in progress,
// so it's better we'll wait before processing
// task = add | exist | del
func (r *RoomService) RoomCreationProgressList(roomId, task string) (bool, error) {
	switch task {
	case "add":
		_, err := r.rc.SAdd(r.ctx, RoomCreationProgressList, roomId).Result()
		if err != nil {
			return false, err
		}
		return true, nil
	case "exist":
		result, err := r.rc.SIsMember(r.ctx, RoomCreationProgressList, roomId).Result()
		if err != nil {
			return false, err
		}
		return result, nil
	case "del":
		_, err := r.rc.SRem(r.ctx, RoomCreationProgressList, roomId).Result()
		if err != nil {
			return false, err
		}
		return true, nil
	}

	return false, errors.New("invalid task")
}

func (r *RoomService) OnAfterRoomClosed(roomId string) {
	// completely remove a room active users list
	_, err := r.ManageActiveUsersList(roomId, "", "delList", 0)
	if err != nil {
		log.Errorln(err)
	}

	// delete blocked users list
	_, err = r.DeleteRoomBlockList(roomId)
	if err != nil {
		log.Errorln(err)
	}

	// completely remove the room key
	_, err = r.ManageRoomWithUsersMetadata(roomId, "", "delList", "")
	if err != nil {
		log.Errorln(err)
	}

	// remove this room from an active room list
	_, err = r.ManageActiveRoomsWithMetadata(roomId, "del", "")
	if err != nil {
		log.Errorln(err)
	}

	// remove from progress, if existed. no need to log if error
	_, _ = r.RoomCreationProgressList(roomId, "del")
}
