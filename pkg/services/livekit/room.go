package livekitservice

import (
	"context"
	"time"

	"github.com/livekit/protocol/livekit"
)

// EndRoom will send API request to livekit
func (s *LivekitService) EndRoom(roomId string) (string, error) {
	data := livekit.DeleteRoomRequest{
		Room: roomId,
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Second*15)
	defer cancel()

	res, err := s.lkc.DeleteRoom(ctx, &data)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "no response received", nil
	}

	return res.String(), nil
}
