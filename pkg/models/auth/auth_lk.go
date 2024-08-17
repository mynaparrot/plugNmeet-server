package authmodel

import (
	"errors"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/webhook"
)

func (m *AuthModel) GenerateLivekitToken(c *plugnmeet.PlugNmeetTokenClaims) (string, error) {
	info, err := m.natsService.GetUserInfo(c.UserId)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "", errors.New("user not found")
	}

	// without any metadata, we won't continue
	if info.Metadata == "" {
		return "", errors.New("empty user metadata")
	}

	return auth.GenerateLivekitAccessToken(m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, m.app.LivekitInfo.TokenValidity, c, info.Metadata)
}

func (m *AuthModel) ValidateLivekitWebhookToken(body []byte, token string) (bool, error) {
	return webhook.VerifyRequest(body, m.app.LivekitInfo.ApiKey, m.app.LivekitInfo.Secret, token)
}
