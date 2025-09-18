package models

import (
	"errors"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AuthModel) GeneratePNMJoinToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, c.UserId, *m.app.Client.TokenValidity, c)
}

func (m *AuthModel) VerifyPlugNmeetAccessToken(token string, withTime bool) (*plugnmeet.PlugNmeetTokenClaims, error) {
	return auth.VerifyPlugNmeetAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, token, withTime)
}

func (m *AuthModel) UnsafeClaimsWithoutVerification(token string) (*plugnmeet.PlugNmeetTokenClaims, error) {
	cl := new(plugnmeet.PlugNmeetTokenClaims)
	tk, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return nil, err
	}

	err = tk.UnsafeClaimsWithoutVerification(cl)
	if err != nil {
		return nil, err
	}

	return cl, nil
}

// RenewPNMToken we'll renew token
func (m *AuthModel) RenewPNMToken(token string, withTime bool) (string, error) {
	claims, err := m.VerifyPlugNmeetAccessToken(token, withTime)
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
