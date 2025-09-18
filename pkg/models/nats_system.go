package models

import (
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
)

func (m *NatsModel) RenewPNMToken(roomId, userId, token string) {
	// to renew token, we can avoid it to check expiry
	// because in may case because of network related issues,
	// the client was not able to renew token
	// as renew request is coming from nats, so it should be secure
	token, err := m.authModel.RenewPNMToken(token, false)
	if err != nil {
		m.logger.Errorln(fmt.Errorf("error renewing pnm token for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
		return
	}

	err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_RENEW_PNM_TOKEN, roomId, token, &userId)
	if err != nil {
		m.logger.Errorln(fmt.Errorf("error sending RESP_RENEW_PNM_TOKEN event for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
	}
}

func (m *NatsModel) GenerateLivekitToken(roomId string, userInfo *plugnmeet.NatsKvUserInfo) (string, error) {
	c := &plugnmeet.PlugNmeetTokenClaims{
		RoomId:  roomId,
		Name:    userInfo.Name,
		UserId:  userInfo.UserId,
		IsAdmin: userInfo.IsAdmin,
	}

	return auth.GenerateLivekitAccessToken(m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, *m.app.Client.TokenValidity, c)
}

func (m *NatsModel) HandleClientPing(roomId, userId string) {
	// check user status
	// if we found offline/disconnected, then we'll update
	//  because the server may receive this join status a bit lately
	// as user has sent ping request, this indicates the user is online
	// OnAfterUserJoined will check the current status and act if the user was not online.
	m.OnAfterUserJoined(roomId, userId)

	err := m.natsService.UpdateUserKeyValue(roomId, userId, natsservice.UserLastPingAt, fmt.Sprintf("%d", time.Now().UnixMilli()))
	if err != nil {
		m.logger.Errorln(fmt.Sprintf("error updating user last ping for %s; roomId: %s; msg: %s", userId, roomId, err.Error()))
	}
}
