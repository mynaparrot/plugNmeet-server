package usermodel

import (
	"errors"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

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
func (m *UserModel) MuteUnMuteTrack(r *plugnmeet.MuteUnMuteTrackReq) error {
	if r.UserId == "all" {
		err := m.muteUnmuteAllMic(r)
		return err
	}

	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		return err
	}

	if p.State != livekit.ParticipantInfo_ACTIVE {
		return errors.New(config.UserNotActive)
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

	_, err = m.lk.MuteUnMuteTrack(r.RoomId, r.UserId, trackSid, r.Muted)
	if err != nil {
		return err
	}

	return nil
}

func (m *UserModel) muteUnmuteAllMic(r *plugnmeet.MuteUnMuteTrackReq) error {
	participants, err := m.lk.LoadParticipants(r.RoomId)
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
				_, _ = m.lk.MuteUnMuteTrack(r.RoomId, p.Identity, trackSid, r.Muted)
			}
		}
	}

	return nil
}
