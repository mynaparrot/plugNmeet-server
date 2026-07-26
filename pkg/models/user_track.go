package models

import (
	"context"
	"fmt"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// MuteUnMuteTrack can be used to mute or unmute track
// if track_sid wasn't send then it will find the microphone track & mute it
// for unmute you'll require enabling "enable_remote_unmute: true" in livekit
// under room settings. For privacy reason we aren't using it.
func (m *UserModel) MuteUnMuteTrack(ctx context.Context, r *plugnmeet.MuteUnMuteTrackReq) error {
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
		return m.muteUnmuteAllMic(ctx, r, log)
	}

	p, err := m.lk.LoadParticipantInfo(r.RoomId, r.UserId)
	if err != nil {
		log.WithError(err).Error("failed to load participant info")
		return fmt.Errorf("footer.notice.failed-to-load-participant-info")
	}

	if p == nil || p.State != livekit.ParticipantInfo_ACTIVE {
		err = config.UserNotActive
		log.WithError(err).Warnln("participant not active")
		return err
	}

	trackSid := r.TrackSid
	targetUserId := r.UserId

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
		// The primary participant has no mic track (e.g., hybrid web twin).
		// Redirect to the [userID]-native publisher participant.
		twinIdentity := config.GetNativeTwinIdentity(r.UserId)
		participants, loadErr := m.lk.LoadParticipants(ctx, r.RoomId)
		if loadErr != nil {
			log.WithError(loadErr).Warn("failed to load room participants for native twin lookup")
		} else {
			for _, tp := range participants {
				if tp.Identity != twinIdentity || tp.State != livekit.ParticipantInfo_ACTIVE {
					continue
				}
				for _, t := range tp.Tracks {
					if t.Source == livekit.TrackSource_MICROPHONE {
						trackSid = t.Sid
						targetUserId = twinIdentity
						log.WithFields(logrus.Fields{
							"twinIdentity": twinIdentity,
							"trackSid":     trackSid,
						}).Infoln("redirecting mute/unmute to native twin")
						break
					}
				}
				break
			}
		}
	}

	if trackSid == "" {
		log.Warnln("No suitable track found to mute/unmute")
		return fmt.Errorf("footer.notice.no-suitable-track-found")
	}

	_, err = m.lk.MuteUnMuteTrack(ctx, r.RoomId, targetUserId, trackSid, r.Muted)
	if err != nil {
		log.WithError(err).Error("failed to mute/unmute track in livekit")
		return fmt.Errorf("footer.notice.failed-to-mute-unmute-track")
	}

	log.Infoln("Successfully muted/unmuted track")
	return nil
}

func (m *UserModel) muteUnmuteAllMic(ctx context.Context, r *plugnmeet.MuteUnMuteTrackReq, log *logrus.Entry) error {
	log.Infoln("request to mute/unmute all microphones")
	participants, err := m.lk.LoadParticipants(ctx, r.RoomId)
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
					if _, err := m.lk.MuteUnMuteTrack(ctx, r.RoomId, p.Identity, t.Sid, r.Muted); err != nil {
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

	log.Info("Successfully processed mute/unmute all microphones request")
	return nil
}
