package natsmodel

import "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

func (m *NatsModel) SendRoomMetadata(roomId string, userId *string) error {
	return m.natsService.BroadcastRoomMetadata(roomId, nil, userId)
}

func (m *NatsModel) UpdateAndSendRoomMetadata(roomId string, meta *plugnmeet.RoomMetadata) error {
	return m.natsService.UpdateAndBroadcastRoomMetadata(roomId, meta)
}
