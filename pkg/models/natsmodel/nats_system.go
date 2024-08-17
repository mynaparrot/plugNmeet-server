package natsmodel

import (
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	log "github.com/sirupsen/logrus"
)

func (m *NatsModel) RenewPNMToken(roomId, userId, token string) error {
	token, err := m.authModel.RenewPNMToken(token)
	if err != nil {
		log.Errorln(err)
		return err
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_PMN_RENEWED_TOKEN, roomId, token, &userId)
	if err != nil {
		return err
	}

	return nil
}

func (m *NatsModel) GenerateLivekitToken(roomId string, userInfo *plugnmeet.NatsKvUserInfo) (string, error) {
	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:  roomId,
		Name:    userInfo.Name,
		UserId:  userInfo.UserId,
		IsAdmin: userInfo.IsAdmin,
	}

	return auth.GenerateLivekitAccessToken(m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, m.app.LivekitInfo.TokenValidity, c, userInfo.Metadata)
}
