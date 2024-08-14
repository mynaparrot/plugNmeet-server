package livekitservice

import "github.com/livekit/protocol/livekit"

// SendData will send a request to livekit for sending data message
func (s *LivekitService) SendData(roomId string, data []byte, dataPacketKind livekit.DataPacket_Kind, destinationUserIds []string) (*livekit.SendDataResponse, error) {
	req := livekit.SendDataRequest{
		Room:                  roomId,
		Data:                  data,
		Kind:                  dataPacketKind,
		DestinationIdentities: destinationUserIds,
	}

	res, err := s.lkc.SendData(s.ctx, &req)
	if err != nil {
		return nil, err
	}

	return res, nil
}
