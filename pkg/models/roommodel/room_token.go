package roommodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
)

func (m *RoomModel) GeneratePNMJoinToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, c.UserId, m.app.LivekitInfo.TokenValidity, c)
}

func (m *RoomModel) VerifyPlugNmeetAccessToken(token string) (*plugnmeet.PlugNmeetTokenClaims, error) {
	return auth.VerifyPlugNmeetAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, token)
}

type RenewTokenReq struct {
	Token string `json:"token"`
}

// DoRenewPlugNmeetToken we'll renew token
func (m *RoomModel) DoRenewPlugNmeetToken(token string) (string, error) {
	claims, err := m.VerifyPlugNmeetAccessToken(token)
	if err != nil {
		return "", err
	}

	// load current information
	p, err := m.rs.ManageActiveUsersList(claims.RoomId, claims.UserId, "get", 0)
	if err != nil {
		return "", err
	}
	if len(p) == 0 {
		return "", errors.New("user isn't online")
	}

	return auth.GeneratePlugNmeetJWTAccessToken(m.app.Client.ApiKey, m.app.Client.Secret, claims.UserId, m.app.LivekitInfo.TokenValidity, claims)
}

func (m *RoomModel) GenerateLivekitToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	metadata, err := m.rs.ManageRoomWithUsersMetadata(c.RoomId, c.UserId, "get", "")
	if err != nil {
		return "", err
	}
	// without any metadata, we won't continue
	if metadata == "" {
		return "", errors.New("empty user metadata")
	}

	return auth.GenerateLivekitAccessToken(m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, m.app.LivekitInfo.TokenValidity, c, metadata)
}

func (m *RoomModel) ValidateLivekitWebhookToken(body []byte, token string) (bool, error) {
	return webhook.VerifyRequest(body, m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, token)
}
