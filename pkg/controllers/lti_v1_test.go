package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/jordic/lti"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
)

const (
	ltiTestApiKey     = "test-api-key"
	ltiTestSecret     = "test-secret-at-least-32-bytes-long!"
	ltiTestSigningURL = "https://example.com/lti/v1"
)

// postLTITestHandler posts body to handler mounted at the landing route and
// returns the status code and the raw body.
func postLTITestHandler(t *testing.T, handler fiber.Handler, body string) (int, string) {
	t.Helper()

	app := fiber.New()
	app.Post("/lti/v1", handler)

	// absolute https URL so that the handler derives the same signing URL that
	// signLTITestBody used
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, ltiTestSigningURL, strings.NewReader(body)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

// ltiErrorMsg decodes the msg field of a sendErrorResponse body.
func ltiErrorMsg(t *testing.T, body string) string {
	t.Helper()

	res := struct {
		Status bool   `json:"status"`
		Msg    string `json:"msg"`
	}{}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatalf("body %q is not an error response: %v", body, err)
	}
	if res.Status {
		t.Fatalf("body %q reports success", body)
	}
	return res.Msg
}

// signLTITestBody builds an LTI 1.0 launch body signed with the given secret.
func signLTITestBody(t *testing.T, secret, consumerKey string, params url.Values) string {
	t.Helper()

	p := lti.NewProvider(secret, ltiTestSigningURL)
	p.Method = "POST"
	p.ConsumerKey = consumerKey
	for k, v := range params {
		p.Add(k, v[0])
	}
	if _, err := p.Sign(); err != nil {
		t.Fatalf("failed to sign request: %v", err)
	}

	signed := p.Params()
	parts := make([]string, 0, len(signed))
	for k := range signed {
		parts = append(parts, k+"="+url.QueryEscape(signed.Get(k)))
	}
	return strings.Join(parts, "&")
}

func newLTILandingTestController() *LtiV1Controller {
	appCfg := &config.AppConfig{
		Client: config.ClientInfo{ApiKey: ltiTestApiKey, Secret: ltiTestSecret},
	}
	rm := models.NewRoomModel(models.RoomModelArgs{
		Ctx:    context.Background(),
		App:    appCfg,
		Logger: logrus.New(),
	})

	return &LtiV1Controller{
		app:        appCfg,
		logger:     logrus.New().WithField("controller", "lti_v1"),
		LtiV1Model: models.NewLtiV1Model(models.LtiV1ModelArgs{App: appCfg, Rm: rm}),
	}
}

// TestHandleLTIV1LandingAnswersCuratedErrors proves that a launch failure never
// reaches the LTI consumer as a model sentinel or dependency error text.
func TestHandleLTIV1LandingAnswersCuratedErrors(t *testing.T) {
	lc := newLTILandingTestController()

	tests := []struct {
		name    string
		body    string
		leak    []string
		wantMsg string
	}{
		{
			name:    "unknown consumer key",
			body:    signLTITestBody(t, ltiTestSecret, "another-consumer-key", url.Values{"user_id": {"user-1"}}),
			leak:    []string{"consumer_key", "invalid consumer_key"},
			wantMsg: config.MsgLTILaunchVerificationFailed,
		},
		{
			name:    "signature mismatch",
			body:    signLTITestBody(t, "another-secret-at-least-32-bytes!!", ltiTestApiKey, url.Values{"user_id": {"user-1"}}),
			leak:    []string{"verification failed", "oauth_signature"},
			wantMsg: config.MsgLTILaunchVerificationFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := postLTITestHandler(t, lc.HandleLTIV1Landing, tt.body)

			if status != fiber.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", status, fiber.StatusUnauthorized)
			}
			if got := ltiErrorMsg(t, body); got != tt.wantMsg {
				t.Fatalf("msg = %q, want %q", got, tt.wantMsg)
			}
			for _, leak := range tt.leak {
				if strings.Contains(body, leak) {
					t.Fatalf("response %q leaks internal detail %q", body, leak)
				}
			}
		})
	}
}

func TestLtiLandingErrorResponseClassification(t *testing.T) {
	dependencyErr := errors.New("dial tcp 10.0.0.5:5432: connect: connection refused")

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{name: "invalid consumer key", err: config.InvalidConsumerKey, wantStatus: fiber.StatusUnauthorized, wantMsg: config.MsgLTILaunchVerificationFailed},
		{name: "wrapped verification failure", err: fmt.Errorf("launch: %w", config.VerificationFailed), wantStatus: fiber.StatusUnauthorized, wantMsg: config.MsgLTILaunchVerificationFailed},
		{name: "missing user identity", err: config.UserIdOrEmailRequired, wantStatus: fiber.StatusBadRequest, wantMsg: config.MsgLTIInvalidRequest},
		{name: "dependency failure", err: dependencyErr, wantStatus: fiber.StatusInternalServerError, wantMsg: config.MsgIntegratorUnexpected},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg := ltiLandingErrorResponse(tt.err)
			if status != tt.wantStatus || msg != tt.wantMsg {
				t.Fatalf("got (%d, %q), want (%d, %q)", status, msg, tt.wantStatus, tt.wantMsg)
			}
			if msg == tt.err.Error() {
				t.Fatalf("msg %q repeats the raw error", msg)
			}
		})
	}
}

// TestLtiRecordingFetchErrorResponse proves that only a NotFoundErr is answered
// as a missing recording; an operational failure gets its own message.
func TestLtiRecordingFetchErrorResponse(t *testing.T) {
	dependencyErr := errors.New("sql: database is closed")

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
		leak       []string
	}{
		{name: "missing recording", err: config.NotFoundErr, wantStatus: fiber.StatusNotFound, wantMsg: config.MsgLTIRecordingNotFound},
		{name: "wrapped missing recording", err: fmt.Errorf("recording: %w", config.NotFoundErr), wantStatus: fiber.StatusNotFound, wantMsg: config.MsgLTIRecordingNotFound},
		{
			name:       "operational failure",
			err:        dependencyErr,
			wantStatus: fiber.StatusInternalServerError,
			wantMsg:    config.MsgLTIRecordingFetchFailed,
			leak:       []string{config.MsgLTIRecordingNotFound, "database"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(c fiber.Ctx) error {
				status, msg := ltiRecordingFetchErrorResponse(tt.err)
				return sendErrorResponse(c, status, msg)
			}
			status, body := postLTITestHandler(t, handler, "")

			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d", status, tt.wantStatus)
			}
			if got := ltiErrorMsg(t, body); got != tt.wantMsg {
				t.Fatalf("msg = %q, want %q", got, tt.wantMsg)
			}
			for _, leak := range tt.leak {
				if strings.Contains(body, leak) {
					t.Fatalf("response %q contains %q", body, leak)
				}
			}
		})
	}
}
