package controllers

import (
	"errors"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nkeys"
	"github.com/sirupsen/logrus"
)

type fakeMicroRequest struct {
	data      []byte
	headers   micro.Headers
	responded []byte
	errCode   string
	errDesc   string
}

func (f *fakeMicroRequest) Respond(data []byte, _ ...micro.RespondOpt) error {
	f.responded = data
	return nil
}

func (f *fakeMicroRequest) RespondJSON(any, ...micro.RespondOpt) error { return nil }

func (f *fakeMicroRequest) Error(code, description string, _ []byte, _ ...micro.RespondOpt) error {
	f.errCode, f.errDesc = code, description
	return nil
}

func (f *fakeMicroRequest) Data() []byte           { return f.data }
func (f *fakeMicroRequest) Headers() micro.Headers { return f.headers }
func (f *fakeMicroRequest) Subject() string        { return "test.subject" }
func (f *fakeMicroRequest) Reply() string          { return "test.reply" }

func newTestNatsAuthController(t *testing.T) (*NatsAuthController, string) {
	t.Helper()

	issuerKeyPair, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatalf("failed to create issuer key pair: %v", err)
	}
	userKeyPair, err := nkeys.CreateUser()
	if err != nil {
		t.Fatalf("failed to create user key pair: %v", err)
	}
	userPublicKey, err := userKeyPair.PublicKey()
	if err != nil {
		t.Fatalf("failed to read user public key: %v", err)
	}

	appCnf := &config.AppConfig{Client: config.ClientInfo{ApiKey: authTestApiKey, Secret: authTestSecret}}
	logger := logrus.New()

	return &NatsAuthController{
		app:           appCnf,
		authModel:     models.NewAuthModel(models.AuthModelArgs{App: appCnf, Logger: logger}),
		issuerKeyPair: issuerKeyPair,
		logger:        logger.WithField("sub-controller", "nats-auth"),
	}, userPublicKey
}

func TestNatsAuthRespondSendsTranslationKey(t *testing.T) {
	ac, userPublicKey := newTestNatsAuthController(t)

	sentinels := []error{
		config.ErrAuthUnexpected,
		config.ErrAuthTokenInvalid,
		config.ErrAuthTokenExpired,
		config.ErrAuthUserBlocked,
		config.ErrAuthUserNotActive,
		config.ErrAuthUserInfoNotFound,
	}

	for _, sentinel := range sentinels {
		t.Run(sentinel.Error(), func(t *testing.T) {
			req := &fakeMicroRequest{headers: micro.Headers{}}
			ac.respond(req, userPublicKey, "NAACCOUNTSERVER", "", sentinel)

			rc, err := jwt.DecodeAuthorizationResponseClaims(string(req.responded))
			if err != nil {
				t.Fatalf("failed to decode authorization response: %v", err)
			}
			if rc.Error != sentinel.Error() {
				t.Fatalf("expected error %q, got %q", sentinel.Error(), rc.Error)
			}
			if strings.ContainsAny(rc.Error, " \t") {
				t.Fatalf("error %q is not a translation key", rc.Error)
			}
		})
	}
}

func TestNatsAuthHandleClaimsMapsInvalidToken(t *testing.T) {
	ac, userPublicKey := newTestNatsAuthController(t)

	rc := jwt.NewAuthorizationRequestClaims(userPublicKey)
	rc.UserNkey = userPublicKey
	rc.ConnectOptions.Token = "invalid-token"

	claims, err := ac.handleClaims(rc)
	if claims != nil {
		t.Fatal("expected no claims")
	}
	if !errors.Is(err, config.ErrAuthTokenInvalid) {
		t.Fatalf("expected %v, got %v", config.ErrAuthTokenInvalid, err)
	}
}
