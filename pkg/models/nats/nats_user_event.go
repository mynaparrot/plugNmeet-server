package natsmodel

func (m *NatsModel) SendUserMetadata(roomId, userId string, toUser *string) error {
	return m.natsService.BroadcastUserMetadata(roomId, userId, nil, toUser)
}

func (m *NatsModel) UpdateAndSendUserMetadata(roomId, userId string) {

}
