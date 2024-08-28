package models

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AuthModel) GeneratePNMJoinToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, c.UserId, *m.app.Client.TokenValidity, c)
}

func (m *AuthModel) VerifyPlugNmeetAccessToken(token string) (*plugnmeet.PlugNmeetTokenClaims, error) {
	return auth.VerifyPlugNmeetAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, token)
}

// RenewPNMToken we'll renew token
func (m *AuthModel) RenewPNMToken(token string) (string, error) {
	claims, err := m.VerifyPlugNmeetAccessToken(token)
	if err != nil {
		return "", err
	}

	status, err := m.natsService.GetRoomUserStatus(claims.RoomId, claims.UserId)
	if err != nil {
		return "", err
	}
	if status == "" {
		return "", errors.New("user not found")
	}

	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, claims.UserId, *m.app.Client.TokenValidity, claims)
}
