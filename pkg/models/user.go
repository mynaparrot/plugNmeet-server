package models

import (
	"database/sql"
	"errors"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugNmeet/pkg/config"
)

type userModel struct {
	db          *sql.DB
	roomService *RoomService
}

func NewUserModel() *userModel {
	return &userModel{
		db:          config.AppCnf.DB,
		roomService: NewRoomService(),
	}
}

func (u *userModel) CommonValidation(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	if isAdmin != true {
		return errors.New("only admin can send this request")
	}
	if roomId == "" {
		return errors.New("no roomId in token")
	}

	return nil
}

type UpdateUserLockSettingsReq struct {
	Sid             string `json:"sid" validate:"required"`
	RoomId          string `json:"room_id" validate:"required"`
	UserId          string `json:"user_id" validate:"required"`
	Service         string `json:"service" validate:"required"`
	Direction       string `json:"direction" validate:"required"`
	RequestedUserId string `json:"-"`
}

func (u *userModel) UpdateUserLockSettings(r *UpdateUserLockSettingsReq) error {
	if r.UserId == "all" {
		err := u.updateLockSettingsAllUsers(r)
		return err
	}

	p, err := u.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	um := updateParticipantLockMetadata{
		participantInfo: p,
		roomId:          r.RoomId,
		service:         r.Service,
		direction:       r.Direction,
	}
	err = u.updateParticipantLockMetadata(um)

	return err
}

func (u *userModel) updateLockSettingsAllUsers(r *UpdateUserLockSettingsReq) error {
	participants, err := u.roomService.LoadParticipantsFromRedis(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		if r.RequestedUserId != p.Identity {
			um := updateParticipantLockMetadata{
				participantInfo: p,
				roomId:          r.RoomId,
				service:         r.Service,
				direction:       r.Direction,
			}
			_ = u.updateParticipantLockMetadata(um)
		}
	}

	// now we'll require updating room settings
	// so that future users can be applied same lock settings
	info, err := u.roomService.LoadRoomInfoFromRedis(r.RoomId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(info.Metadata))
	copy(meta, info.Metadata)

	m := new(RoomMetadata)
	_ = json.Unmarshal(meta, m)

	l := u.changeLockSettingsMetadata(r.Service, r.Direction, &m.DefaultLockSettings)
	m.DefaultLockSettings = *l

	newMeta, _ := json.Marshal(m)
	_, err = u.roomService.UpdateRoomMetadata(r.RoomId, string(newMeta))

	return err
}

type updateParticipantLockMetadata struct {
	participantInfo *livekit.ParticipantInfo
	roomId          string
	service         string
	direction       string
}

func (u *userModel) updateParticipantLockMetadata(um updateParticipantLockMetadata) error {
	if um.participantInfo.State.String() == "ACTIVE" {
		meta := make([]byte, len(um.participantInfo.Metadata))
		copy(meta, um.participantInfo.Metadata)

		m := new(UserMetadata)
		_ = json.Unmarshal(meta, m)
		l := u.changeLockSettingsMetadata(um.service, um.direction, &m.LockSettings)
		m.LockSettings = *l

		newMeta, _ := json.Marshal(m)
		_, err := u.roomService.UpdateParticipantMetadata(um.roomId, um.participantInfo.Identity, string(newMeta))

		return err
	}

	return errors.New("user isn't active now")
}

func (u *userModel) changeLockSettingsMetadata(service string, direction string, l *LockSettings) *LockSettings {
	lock := new(bool)
	if direction == "lock" {
		*lock = true
	}

	switch service {
	case "mic":
		l.LockMicrophone = lock
	case "webcam":
		l.LockWebcam = lock
	case "screenShare":
		l.LockScreenSharing = lock
	case "chat":
		l.LockChat = lock
	case "sendChatMsg":
		l.LockChatSendMessage = lock
	case "chatFile":
		l.LockChatFileShare = lock
	case "privateChat":
		l.LockPrivateChat = lock
	case "whiteboard":
		l.LockWhiteboard = lock
	case "sharedNotepad":
		l.LockSharedNotepad = lock
	}

	return l
}

type MuteUnMuteTrackReq struct {
	Sid             string `json:"sid" validate:"required"`
	RoomId          string `json:"room_id" validate:"required"`
	UserId          string `json:"user_id" validate:"required"`
	TrackSid        string `json:"track_sid"`
	Muted           bool   `json:"muted"`
	RequestedUserId string `json:"-"`
}

// MuteUnMuteTrack can be used to mute or unmute track
// if track_sid wasn't send then it will find the microphone track & mute it
// for unmute you'll require enabling "enable_remote_unmute: true" in livekit
// under room settings. For privacy reason we aren't using it.
func (u *userModel) MuteUnMuteTrack(r *MuteUnMuteTrackReq) error {
	if r.UserId == "all" {
		err := u.muteUnmuteAllMic(r)
		return err
	}

	p, err := u.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State.String() != "ACTIVE" {
		return errors.New("user isn't active now")
	}
	trackSid := r.TrackSid

	if trackSid == "" {
		for _, t := range p.Tracks {
			if t.Source.String() == livekit.TrackSource_MICROPHONE.String() {
				trackSid = t.Sid
				break
			}
		}
	}

	_, err = u.roomService.MuteUnMuteTrack(r.RoomId, r.UserId, trackSid, r.Muted)
	if err != nil {
		return err
	}

	return nil
}

func (u *userModel) muteUnmuteAllMic(r *MuteUnMuteTrackReq) error {
	participants, err := u.roomService.LoadParticipantsFromRedis(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		if p.State.String() == "ACTIVE" && p.Identity != r.RequestedUserId {
			trackSid := ""
			for _, t := range p.Tracks {
				if t.Source.String() == livekit.TrackSource_MICROPHONE.String() {
					trackSid = t.Sid
					break
				}
			}

			if trackSid != "" {
				_, _ = u.roomService.MuteUnMuteTrack(r.RoomId, p.Identity, trackSid, r.Muted)
			}
		}
	}

	return nil
}

type RemoveParticipantReq struct {
	Sid       string `json:"sid" validate:"required"`
	RoomId    string `json:"room_id" validate:"required"`
	UserId    string `json:"user_id" validate:"required"`
	Msg       string `json:"msg" validate:"required"`
	BlockUser bool   `json:"block_user"`
}

func (u *userModel) RemoveParticipant(r *RemoveParticipantReq) error {
	p, err := u.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State.String() != "ACTIVE" {
		return errors.New("user isn't active now")
	}

	// send message to user first
	_ = NewDataMessage(&DataMessageReq{
		MsgType: "ALERT",
		Msg:     r.Msg,
		RoomId:  r.RoomId,
		SendTo:  []string{p.Sid},
	})

	// now remove
	_, err = u.roomService.RemoveParticipant(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	// finally check if requested to block as well as
	if r.BlockUser {
		_, _ = u.roomService.AddUserToBlockList(r.RoomId, r.UserId)
	}

	return nil
}

type SwitchPresenterReq struct {
	Task            string `json:"task" validate:"required"`
	UserId          string `json:"user_id" validate:"required"`
	RoomId          string
	RequestedUserId string
}

func (u *userModel) SwitchPresenter(r *SwitchPresenterReq) error {
	participants, err := u.roomService.LoadParticipantsFromRedis(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		meta := make([]byte, len(p.Metadata))
		copy(meta, p.Metadata)

		m := new(UserMetadata)
		_ = json.Unmarshal(meta, m)

		if r.Task == "promote" {
			if m.IsPresenter {
				// demote current presenter from presenter
				m.IsPresenter = false
				err = u.updateUserMetadata(m, r.RoomId, p.Identity)
				if err != nil {
					return errors.New("can't demote current presenter")
				}
			}
		} else if r.Task == "demote" {
			if p.Identity == r.RequestedUserId {
				// we'll update requested user as presenter
				// otherwise in the session there won't have any presenter
				m.IsPresenter = true
				err = u.updateUserMetadata(m, r.RoomId, p.Identity)
				if err != nil {
					return errors.New("can't change alternative presenter")
				}
			}
		}
	}

	// if everything goes well in top then we'll go ahead
	p, err := u.roomService.LoadParticipantInfoFromRedis(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(p.Metadata))
	copy(meta, p.Metadata)

	m := new(UserMetadata)
	_ = json.Unmarshal(meta, m)

	if r.Task == "promote" {
		m.IsPresenter = true
		err = u.updateUserMetadata(m, r.RoomId, p.Identity)
		if err != nil {
			return errors.New("can't promote to presenter")
		}
	} else if r.Task == "demote" {
		m.IsPresenter = false
		err = u.updateUserMetadata(m, r.RoomId, p.Identity)
		if err != nil {
			return errors.New("can't demote to presenter. try again")
		}
	}

	return nil
}

func (u *userModel) updateUserMetadata(meta *UserMetadata, roomId, userId string) error {
	newMeta, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = u.roomService.UpdateParticipantMetadata(roomId, userId, string(newMeta))
	return err
}
