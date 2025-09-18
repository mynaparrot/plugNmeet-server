package models

import (
	"fmt"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
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
	log := m.logger.WithFields(logrus.Fields{
		"roomId":          r.RoomId,
		"userId":          r.UserId,
		"trackSid":        r.TrackSid,
		"muted":           r.Muted,
		"requestedUserId": r.RequestedUserId,
		"method":          "MuteUnMuteTrack",
	})
	log.Infoln("request to mute/unmute track")

	if r.UserId == "all" {
		return m.muteUnmuteAllMic(r, log)
	}

	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Errorln("failed to load participant info")
		return err
	}

	if p == nil || p.State != livekit.ParticipantInfo_ACTIVE {
		err = fmt.Errorf(config.UserNotActive)
		log.WithError(err).Warnln("participant not active")
		return err
	}
	trackSid := r.TrackSid

	if trackSid == "" {
		log.Infoln("no trackSid provided, searching for microphone track")
		for _, t := range p.Tracks {
			if t.Source == livekit.TrackSource_MICROPHONE {
				trackSid = t.Sid
				log.WithField("foundTrackSid", trackSid).Infoln("found microphone track")
				break
			}
		}
	}

	if trackSid == "" {
		err = fmt.Errorf("no suitable track found to mute/unmute")
		log.WithError(err).Warnln()
		return err
	}

	_, err = m.lk.MuteUnMuteTrack(r.RoomId, r.UserId, trackSid, r.Muted)
	if err != nil {
		log.WithError(err).Errorln("failed to mute/unmute track in livekit")
		return err
	}

	log.Infoln("successfully muted/unmuted track")
	return nil
}

func (m *UserModel) muteUnmuteAllMic(r *plugnmeet.MuteUnMuteTrackReq, log *logrus.Entry) error {
	log.Infoln("request to mute/unmute all microphones")
	participants, err := m.lk.LoadParticipants(r.RoomId)
	if err != nil {
		return err
	}
	if participants == nil {
		err = fmt.Errorf("no active users found")
		log.WithError(err).Warnln()
		return err
	}

	for _, p := range participants {
		if p.State == livekit.ParticipantInfo_ACTIVE && p.Identity != r.RequestedUserId {
			for _, t := range p.Tracks {
				if t.Source == livekit.TrackSource_MICROPHONE {
					if _, err := m.lk.MuteUnMuteTrack(r.RoomId, p.Identity, t.Sid, r.Muted); err != nil {
						log.WithFields(logrus.Fields{
							"targetUserId":   p.Identity,
							"targetTrackSid": t.Sid,
						}).WithError(err).Errorln("failed to mute/unmute track for participant")
					}
					break
				}
			}
		}
	}

	log.Info("successfully processed mute/unmute all microphones request")
	return nil
}
