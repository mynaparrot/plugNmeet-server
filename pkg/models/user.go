package models

import (
	"database/sql"
	"errors"
	"github.com/gofiber/fiber/v2"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type UserModel struct {
	db          *sql.DB
	roomService *RoomService
}

func NewUserModel() *UserModel {
	return &UserModel{
		db:          config.AppCnf.DB,
		roomService: NewRoomService(),
	}
}

func (u *UserModel) CommonValidation(c *fiber.Ctx) error {
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

func (u *UserModel) UpdateUserLockSettings(r *plugnmeet.UpdateUserLockSettingsReq) error {
	if r.UserId == "all" {
		err := u.updateLockSettingsAllUsers(r)
		return err
	}

	p, err := u.roomService.LoadParticipantInfo(r.RoomId, r.UserId)
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

func (u *UserModel) updateLockSettingsAllUsers(r *plugnmeet.UpdateUserLockSettingsReq) error {
	participants, err := u.roomService.LoadParticipants(r.RoomId)
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
	info, err := u.roomService.LoadRoomInfo(r.RoomId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(info.Metadata))
	copy(meta, info.Metadata)

	m, _ := u.roomService.UnmarshalRoomMetadata(string(meta))

	l := u.changeLockSettingsMetadata(r.Service, r.Direction, m.DefaultLockSettings)
	m.DefaultLockSettings = l

	_, err = u.roomService.UpdateRoomMetadataByStruct(r.RoomId, m)

	return err
}

type updateParticipantLockMetadata struct {
	participantInfo *livekit.ParticipantInfo
	roomId          string
	service         string
	direction       string
}

func (u *UserModel) updateParticipantLockMetadata(um updateParticipantLockMetadata) error {
	if um.participantInfo.State == livekit.ParticipantInfo_ACTIVE {
		meta := make([]byte, len(um.participantInfo.Metadata))
		copy(meta, um.participantInfo.Metadata)

		m, _ := u.roomService.UnmarshalParticipantMetadata(string(meta))
		l := u.changeLockSettingsMetadata(um.service, um.direction, m.LockSettings)
		m.LockSettings = l

		_, err := u.roomService.UpdateParticipantMetadataByStruct(um.roomId, um.participantInfo.Identity, m)
		return err
	}

	return errors.New("user isn't active now")
}

func (u *UserModel) changeLockSettingsMetadata(service string, direction string, l *plugnmeet.LockSettings) *plugnmeet.LockSettings {
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
func (u *UserModel) MuteUnMuteTrack(r *plugnmeet.MuteUnMuteTrackReq) error {
	if r.UserId == "all" {
		err := u.muteUnmuteAllMic(r)
		return err
	}

	p, err := u.roomService.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State != livekit.ParticipantInfo_ACTIVE {
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

func (u *UserModel) muteUnmuteAllMic(r *plugnmeet.MuteUnMuteTrackReq) error {
	participants, err := u.roomService.LoadParticipants(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		if p.State == livekit.ParticipantInfo_ACTIVE && p.Identity != r.RequestedUserId {
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

func (u *UserModel) RemoveParticipant(r *plugnmeet.RemoveParticipantReq) error {
	p, err := u.roomService.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State != livekit.ParticipantInfo_ACTIVE {
		return errors.New("user isn't active now")
	}

	// send message to user first
	dm := NewDataMessageModel()
	_ = dm.SendDataMessage(&plugnmeet.DataMessageReq{
		MsgBodyType: plugnmeet.DataMsgBodyType_ALERT,
		Msg:         r.Msg,
		RoomId:      r.RoomId,
		SendTo:      []string{p.Identity},
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

func (u *UserModel) SwitchPresenter(r *plugnmeet.SwitchPresenterReq) error {
	participants, err := u.roomService.LoadParticipants(r.RoomId)
	if err != nil {
		return err
	}

	for _, p := range participants {
		meta := make([]byte, len(p.Metadata))
		copy(meta, p.Metadata)

		m, _ := u.roomService.UnmarshalParticipantMetadata(string(meta))

		if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
			if m.IsPresenter {
				// demote current presenter from presenter
				m.IsPresenter = false
				_, err = u.roomService.UpdateParticipantMetadataByStruct(r.RoomId, p.Identity, m)
				if err != nil {
					return errors.New("can't demote current presenter")
				}
			}
		} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
			if p.Identity == r.RequestedUserId {
				// we'll update requested user as presenter
				// otherwise in the session there won't have any presenter
				m.IsPresenter = true
				_, err = u.roomService.UpdateParticipantMetadataByStruct(r.RoomId, p.Identity, m)
				if err != nil {
					return errors.New("can't change alternative presenter")
				}
			}
		}
	}

	// if everything goes well in top then we'll go ahead
	p, err := u.roomService.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}
	meta := make([]byte, len(p.Metadata))
	copy(meta, p.Metadata)

	m, _ := u.roomService.UnmarshalParticipantMetadata(string(meta))

	if r.Task == plugnmeet.SwitchPresenterTask_PROMOTE {
		m.IsPresenter = true
		_, err = u.roomService.UpdateParticipantMetadataByStruct(r.RoomId, p.Identity, m)
		if err != nil {
			return errors.New("can't promote to presenter")
		}
	} else if r.Task == plugnmeet.SwitchPresenterTask_DEMOTE {
		m.IsPresenter = false
		_, err = u.roomService.UpdateParticipantMetadataByStruct(r.RoomId, p.Identity, m)
		if err != nil {
			return errors.New("can't demote to presenter. try again")
		}
	}

	return nil
}
