package models

import (
	"errors"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AuthModel) GeneratePNMJoinToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, c.UserId, *m.app.Client.TokenValidity, c)
}

func (m *AuthModel) VerifyPlugNmeetAccessToken(token string, gracefulPeriod time.Duration) (*plugnmeet.PlugNmeetTokenClaims, error) {
	return auth.VerifyPlugNmeetAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, token, gracefulPeriod)
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
func (m *AuthModel) RenewPNMToken(token string, gracefulPeriod time.Duration) (string, error) {
	claims, err := m.VerifyPlugNmeetAccessToken(token, gracefulPeriod)
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
