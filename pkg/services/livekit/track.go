package livekitservice

import (
	"context"
	"time"

	"github.com/livekit/protocol/livekit"
)

// MuteUnMuteTrack can be used to mute/unmute track. This will send request to livekit
func (s *LivekitService) MuteUnMuteTrack(roomId string, userId string, trackSid string, muted bool) (*livekit.MuteRoomTrackResponse, error) {
	data := livekit.MuteRoomTrackRequest{
		Room:     roomId,
		Identity: userId,
		TrackSid: trackSid,
		Muted:    muted,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*10)
	defer cancel()

	res, err := s.lkc.MutePublishedTrack(ctx, &data)
	if err != nil {
		return nil, err
	}

	return res, err
}
