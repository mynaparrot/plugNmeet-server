package models

import (
	"context"
	"errors"
	"github.com/go-redis/redis/v8"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
)

const (
	BlockedUsersList = "pnm:block_users_list:"
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

func (r *RoomService) LoadRoomInfo(roomId string) (*livekit.Room, error) {
	req := livekit.ListRoomsRequest{
		Names: []string{
			roomId,
		},
	}

	res, err := r.livekitClient.ListRooms(r.ctx, &req)
	if err != nil {
		log.Errorln(err)
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

func (r *RoomService) LoadParticipants(roomId string) ([]*livekit.ParticipantInfo, error) {
	req := livekit.ListParticipantsRequest{
		Room: roomId,
	}
	res, err := r.livekitClient.ListParticipants(r.ctx, &req)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	return res.Participants, nil
}

func (r *RoomService) LoadParticipantInfo(roomId string, identity string) (*livekit.ParticipantInfo, error) {
	req := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: identity,
	}

	participant, err := r.livekitClient.GetParticipant(r.ctx, &req)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	if participant == nil {
		return nil, errors.New("participant not found")
	}

	return participant, nil
}

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
		log.Errorln(err)
		return nil, err
	}

	return room, nil
}

func (r *RoomService) UpdateRoomMetadata(roomId string, metadata string) (*livekit.Room, error) {
	data := livekit.UpdateRoomMetadataRequest{
		Room:     roomId,
		Metadata: metadata,
	}

	room, err := r.livekitClient.UpdateRoomMetadata(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return room, nil
}

func (r *RoomService) EndRoom(roomId string) (string, error) {
	data := livekit.DeleteRoomRequest{
		Room: roomId,
	}

	res, err := r.livekitClient.DeleteRoom(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return "", err
	}

	return res.String(), nil
}

func (r *RoomService) UpdateParticipantMetadata(roomId string, userId string, metadata string) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:     roomId,
		Identity: userId,
		Metadata: metadata,
	}

	participant, err := r.livekitClient.UpdateParticipant(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return participant, nil
}

func (r *RoomService) UpdateParticipantPermission(roomId string, userId string, permission *livekit.ParticipantPermission) (*livekit.ParticipantInfo, error) {
	data := livekit.UpdateParticipantRequest{
		Room:       roomId,
		Identity:   userId,
		Permission: permission,
	}

	participant, err := r.livekitClient.UpdateParticipant(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return participant, nil
}

func (r *RoomService) RemoveParticipant(roomId string, userId string) (*livekit.RemoveParticipantResponse, error) {
	data := livekit.RoomParticipantIdentity{
		Room:     roomId,
		Identity: userId,
	}

	res, err := r.livekitClient.RemoveParticipant(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return res, err
}

func (r *RoomService) MuteUnMuteTrack(roomId string, userId string, trackSid string, muted bool) (*livekit.MuteRoomTrackResponse, error) {
	data := livekit.MuteRoomTrackRequest{
		Room:     roomId,
		Identity: userId,
		TrackSid: trackSid,
		Muted:    muted,
	}

	res, err := r.livekitClient.MutePublishedTrack(r.ctx, &data)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return res, err
}

func (r *RoomService) SendData(roomId string, data []byte, dataPacket_Kind livekit.DataPacket_Kind, destinationSids []string) (*livekit.SendDataResponse, error) {
	req := livekit.SendDataRequest{
		Room:            roomId,
		Data:            data,
		Kind:            dataPacket_Kind,
		DestinationSids: destinationSids,
	}

	res, err := r.livekitClient.SendData(r.ctx, &req)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return res, nil
}

func (r *RoomService) AddUserToBlockList(roomId, userId string) (int64, error) {
	key := BlockedUsersList + roomId
	return r.rc.SAdd(r.ctx, key, userId).Result()
}

func (r *RoomService) IsUserExistInBlockList(roomId, userId string) bool {
	key := BlockedUsersList + roomId
	exist, err := r.rc.SIsMember(r.ctx, key, userId).Result()
	if err != nil {
		return false
	}
	return exist
}

func (r *RoomService) DeleteRoomBlockList(roomId string) (int64, error) {
	key := BlockedUsersList + roomId
	return r.rc.Del(r.ctx, key).Result()
}

func (r *RoomService) LoadRoomWithMetadata(roomId string) (*livekit.Room, *plugnmeet.RoomMetadata, error) {
	room, err := r.LoadRoomInfo(roomId)
	if err != nil {
		return nil, nil, err
	}

	if room.Metadata == "" {
		return room, nil, errors.New("empty metadata")
	}

	meta := new(plugnmeet.RoomMetadata)
	err = json.Unmarshal([]byte(room.Metadata), meta)
	if err != nil {
		log.Errorln(err)
		return room, nil, err
	}

	return room, meta, nil
}

func (r *RoomService) UpdateRoomMetadataByStruct(roomId string, meta *plugnmeet.RoomMetadata) (*livekit.Room, error) {
	marshal, err := json.Marshal(meta)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	room, err := r.UpdateRoomMetadata(roomId, string(marshal))
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return room, nil
}

func (r *RoomService) LoadParticipantWithMetadata(roomId, userId string) (*livekit.ParticipantInfo, *plugnmeet.UserMetadata, error) {
	p, err := r.LoadParticipantInfo(roomId, userId)
	if err != nil {
		return nil, nil, err
	}

	meta := new(plugnmeet.UserMetadata)
	err = json.Unmarshal([]byte(p.Metadata), meta)
	if err != nil {
		log.Errorln(err)
		return p, nil, err
	}

	return p, meta, nil
}

func (r *RoomService) UpdateParticipantMetadataByStruct(roomId, userId string, meta *plugnmeet.UserMetadata) (*livekit.ParticipantInfo, error) {
	marshal, err := json.Marshal(meta)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	p, err := r.UpdateParticipantMetadata(roomId, userId, string(marshal))
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	return p, nil
}
