package models

import (
	"errors"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func TestTokenVerifyError(t *testing.T) {
	const apiKey = "test-api-key"
	const secret = "test-secret-that-is-long-enough-for-hs256"

	expiredToken, err := auth.GeneratePlugNmeetJWTAccessToken(apiKey, secret, "user-1", -time.Hour, &plugnmeet.PlugNmeetTokenClaims{
		RoomId: "room-1",
		UserId: "user-1",
	})
	if err != nil {
		t.Fatalf("failed to generate expired token: %v", err)
	}

	appCnf := &config.AppConfig{Client: config.ClientInfo{ApiKey: apiKey, Secret: secret}}
	authModel := NewAuthModel(AuthModelArgs{App: appCnf, Logger: logrus.New()})

	_, verifyErr := authModel.VerifyPlugNmeetAccessToken(expiredToken, 0)
	if verifyErr == nil {
		t.Fatal("expected an expired token error")
	}
	if err := TokenVerifyError(verifyErr); !errors.Is(err, config.ErrAuthTokenExpired) {
		t.Fatalf("expected %v, got %v", config.ErrAuthTokenExpired, err)
	}

	invalidErr := errors.New("signature is invalid")
	if err := TokenVerifyError(invalidErr); !errors.Is(err, config.ErrAuthTokenInvalid) {
		t.Fatalf("expected %v, got %v", config.ErrAuthTokenInvalid, err)
	}
}
