package models

import (
	"encoding/json"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"sync"
	"time"
)

type RoomCreateReq struct {
	RoomId          string       `json:"room_id" validate:"required,require-valid-Id"`
	EmptyTimeout    uint32       `json:"empty_timeout" validate:"numeric"`
	MaxParticipants uint32       `json:"max_participants" validate:"numeric"`
	RoomMetadata    RoomMetadata `json:"metadata" validate:"required"`
}

type RoomMetadata struct {
	RoomTitle           string             `json:"room_title" validate:"required"`
	WelcomeMessage      string             `json:"welcome_message"`
	IsRecording         bool               `json:"is_recording"`
	IsActiveRTMP        bool               `json:"is_active_rtmp"`
	WebhookUrl          string             `json:"webhook_url"`
	Features            RoomCreateFeatures `json:"room_features"`
	DefaultLockSettings LockSettings       `json:"default_lock_settings"`
}

type RoomCreateFeatures struct {
	AllowWebcams               bool                  `json:"allow_webcams"`
	MuteOnStart                bool                  `json:"mute_on_start"`
	AllowScreenShare           bool                  `json:"allow_screen_share"`
	AllowRecording             bool                  `json:"allow_recording"`
	AllowRTMP                  bool                  `json:"allow_rtmp"`
	AllowViewOtherWebcams      bool                  `json:"allow_view_other_webcams"`
	AllowViewOtherParticipants bool                  `json:"allow_view_other_users_list"`
	AdminOnlyWebcams           bool                  `json:"admin_only_webcams"`
	ChatFeatures               ChatFeatures          `json:"chat_features"`
	SharedNotePadFeatures      SharedNotePadFeatures `json:"shared_note_pad_features"`
	WhiteboardFeatures         WhiteboardFeatures    `json:"whiteboard_features"`
}

type ChatFeatures struct {
	AllowChat        bool     `json:"allow_chat"`
	AllowFileUpload  bool     `json:"allow_file_upload"`
	AllowedFileTypes []string `json:"allowed_file_types,omitempty"`
	MaxFileSize      int      `json:"max_file_size,omitempty"`
}

type SharedNotePadFeatures struct {
	AllowedSharedNotePad bool   `json:"allowed_shared_note_pad"`
	IsActive             bool   `json:"is_active"`
	Visible              bool   `json:"visible"`
	NodeId               string `json:"node_id"`
	Host                 string `json:"host"`
	NotePadId            string `json:"note_pad_id"` // the shared session Id
	ReadOnlyPadId        string `json:"read_only_pad_id"`
}

type WhiteboardFeatures struct {
	AllowedWhiteboard bool `json:"allowed_whiteboard"`
	Visible           bool `json:"visible"`
}

type RoomEndReq struct {
	RoomId string `json:"room_id" validate:"required,require-valid-Id"`
}

type IsRoomActiveReq struct {
	RoomId string `json:"room_id" validate:"required,require-valid-Id"`
}

type GetRoomInfoReq struct {
	RoomId string `json:"room_id" validate:"required,require-valid-Id"`
}

type ActiveRoomInfoRes struct {
	RoomInfo         *ActiveRoomInfo
	ParticipantsInfo []*livekit.ParticipantInfo
}

type roomAuthModel struct {
	mux *sync.RWMutex
	rs  *RoomService
}

func NewRoomAuthModel() *roomAuthModel {
	return &roomAuthModel{
		mux: &sync.RWMutex{},
		rs:  NewRoomService(),
	}
}

func (am *roomAuthModel) CreateRoom(r *RoomCreateReq) (bool, string, *livekit.Room) {
	am.mux.Lock()
	defer am.mux.Unlock()

	m := NewRoomModel()
	rs := NewRoomService()
	roomDbInfo, _ := m.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id > 0 {
		rf, err := rs.LoadRoomInfoFromRedis(r.RoomId)
		if err != nil {
			_, err = m.UpdateRoomStatus(&RoomInfo{
				RoomId:    r.RoomId,
				IsRunning: 0,
				Ended:     time.Now().Format("2006-01-02 15:04:05"),
			})
			return false, "can't create room. try again", nil
		}
		return true, "room already exists", rf
	}

	// we'll disable if SharedNotePad isn't enable in config
	if !config.AppCnf.SharedNotePad.Enabled {
		r.RoomMetadata.Features.SharedNotePadFeatures.AllowedSharedNotePad = false
	}
	if r.RoomMetadata.Features.WhiteboardFeatures.AllowedWhiteboard {
		r.RoomMetadata.Features.WhiteboardFeatures.Visible = true
	}

	meta, err := json.Marshal(r.RoomMetadata)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	room, err := rs.CreateRoom(r.RoomId, r.EmptyTimeout, r.MaxParticipants, string(meta))
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	ri := &RoomInfo{
		RoomTitle:          r.RoomMetadata.RoomTitle,
		RoomId:             room.Name,
		Sid:                room.Sid,
		JoinedParticipants: 0,
		IsRunning:          1,
		CreationTime:       room.CreationTime,
		WebhookUrl:         r.RoomMetadata.WebhookUrl,
	}

	_, err = m.InsertRoomData(ri)
	if err != nil {
		return false, "Error: " + err.Error(), nil
	}

	return true, "room created", room
}

func (am *roomAuthModel) IsRoomActive(r *IsRoomActiveReq) (bool, string) {
	am.mux.RLock()
	defer am.mux.RUnlock()

	m := NewRoomModel()
	roomDbInfo, _ := m.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room is not active"
	}

	// let's make sure room actually active
	_, err := am.rs.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		// room isn't active. Change status
		_, _ = m.UpdateRoomStatus(&RoomInfo{
			RoomId:    r.RoomId,
			IsRunning: 0,
			Ended:     time.Now().Format("2006-01-02 15:04:05"),
		})
		return false, "room is not active"
	}

	return true, "room is active"
}

func (am *roomAuthModel) GetActiveRoomInfo(r *IsRoomActiveReq) (bool, string, *ActiveRoomInfoRes) {
	am.mux.RLock()
	defer am.mux.RUnlock()

	m := NewRoomModel()
	roomDbInfo, _ := m.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "no room found", nil
	}

	rs := NewRoomService()
	rrr, err := rs.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return false, err.Error(), nil
	}

	res := new(ActiveRoomInfoRes)
	res.RoomInfo = &ActiveRoomInfo{
		RoomTitle:          roomDbInfo.RoomTitle,
		RoomId:             roomDbInfo.RoomId,
		Sid:                roomDbInfo.Sid,
		JoinedParticipants: roomDbInfo.JoinedParticipants,
		IsRunning:          roomDbInfo.IsRunning,
		IsRecording:        roomDbInfo.IsRecording,
		IsActiveRTMP:       roomDbInfo.IsActiveRTMP,
		WebhookUrl:         roomDbInfo.WebhookUrl,
		CreationTime:       roomDbInfo.CreationTime,
		Metadata:           rrr.Metadata,
	}
	res.ParticipantsInfo, _ = rs.LoadParticipantsFromRedis(roomDbInfo.RoomId)

	return true, "success", res
}

func (am *roomAuthModel) GetActiveRoomsInfo() (bool, string, []*ActiveRoomInfoRes) {
	am.mux.RLock()
	defer am.mux.RUnlock()

	m := NewRoomModel()
	rs := NewRoomService()
	roomsInfo, err := m.GetActiveRoomsInfo()

	if err != nil {
		return false, "no active room found", nil
	}

	if len(roomsInfo) == 0 {
		return false, "no active room found", nil
	}

	var res []*ActiveRoomInfoRes
	for _, r := range roomsInfo {
		roomInfo := r
		i := new(ActiveRoomInfoRes)
		i.RoomInfo = &roomInfo

		participants, err := rs.LoadParticipantsFromRedis(r.RoomId)
		if err == nil {
			i.ParticipantsInfo = participants
		}

		rri, err := rs.LoadRoomInfoFromRedis(r.RoomId)
		if err == nil {
			i.RoomInfo.Metadata = rri.Metadata
		}

		res = append(res, i)
	}

	return true, "success", res
}

func (am *roomAuthModel) EndRoom(r *RoomEndReq) (bool, string) {
	am.mux.Lock()
	defer am.mux.Unlock()

	m := NewRoomModel()
	rs := NewRoomService()
	roomDbInfo, _ := m.GetRoomInfo(r.RoomId, "", 1)

	if roomDbInfo.Id == 0 {
		return false, "room not active"
	}

	_, err := rs.EndRoom(r.RoomId)
	if err != nil {
		return false, "can't end room"
	}

	_, _ = m.UpdateRoomStatus(&RoomInfo{
		RoomId:    r.RoomId,
		IsRunning: 0,
		Ended:     time.Now().Format("2006-01-02 15:04:05"),
	})

	return true, "success"
}

type ChangeVisibilityRes struct {
	RoomId            string `json:"room_id"`
	VisibleNotepad    *bool  `json:"visible_notepad,omitempty"`
	VisibleWhiteBoard *bool  `json:"visible_white_board,omitempty"`
}

func (am *roomAuthModel) ChangeVisibility(r *ChangeVisibilityRes) (bool, string) {
	room, err := am.rs.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return false, err.Error()
	}

	m := make([]byte, len(room.Metadata))
	copy(m, room.Metadata)

	roomMeta := new(RoomMetadata)
	err = json.Unmarshal(m, roomMeta)
	if err != nil {
		return false, err.Error()
	}

	if r.VisibleWhiteBoard != nil {
		roomMeta.Features.WhiteboardFeatures.Visible = *r.VisibleWhiteBoard
	}
	if r.VisibleNotepad != nil {
		roomMeta.Features.SharedNotePadFeatures.Visible = *r.VisibleNotepad
	}

	metadata, _ := json.Marshal(roomMeta)
	_, err = am.rs.UpdateRoomMetadata(r.RoomId, string(metadata))

	if err != nil {
		return false, err.Error()
	}

	return true, "success"
}
