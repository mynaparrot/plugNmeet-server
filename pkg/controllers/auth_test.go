package controllers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
	logrusTest "github.com/sirupsen/logrus/hooks/test"
	"google.golang.org/protobuf/proto"
)

const (
	authTestApiKey = "test-api-key"
	authTestSecret = "test-secret"
)

type authJSONResponse struct {
	Status     bool   `json:"status"`
	Msg        string `json:"msg"`
	StatusCode string `json:"status_code"`
}

func newAuthTestController() *AuthController {
	appCnf := &config.AppConfig{Client: config.ClientInfo{ApiKey: authTestApiKey, Secret: authTestSecret}}
	logger := logrus.New()
	return &AuthController{
		AppConfig: appCnf,
		AuthModel: models.NewAuthModel(models.AuthModelArgs{App: appCnf, Logger: logger}),
		Logger:    logger.WithField("controller", "auth"),
	}
}

func newAuthHeaderCheckApp() *fiber.App {
	ac := newAuthTestController()
	app := fiber.New()
	app.Get("/auth/room/create", ac.HandleAuthHeaderCheck, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})
	return app
}

func newVerifyTokenApp() *fiber.App {
	ac := newAuthTestController()
	app := fiber.New()
	app.Post("/api/verifyToken", ac.HandleVerifyHeaderToken, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})
	return app
}

func requestHeaders(pairs ...string) map[string]string {
	headers := make(map[string]string, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		headers[pairs[i]] = pairs[i+1]
	}
	return headers
}

func sendAuthTestRequest(t *testing.T, app *fiber.App, method, path string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func decodeAuthJSONResponse(t *testing.T, resp *http.Response) authJSONResponse {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if resp.StatusCode < 400 {
		t.Fatalf("expected a non-2xx status, got %d", resp.StatusCode)
	}
	var res authJSONResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("failed to decode response %q: %v", body, err)
	}
	return res
}

func TestHandleAuthHeaderCheckFailures(t *testing.T) {
	app := newAuthHeaderCheckApp()

	tests := []struct {
		name     string
		headers  map[string]string
		wantHTTP int
		wantMsg  error
		wantCode plugnmeet.StatusCode
	}{
		{
			name:     "missing api key",
			headers:  nil,
			wantHTTP: fiber.StatusBadRequest,
			wantMsg:  config.ErrAuthS2SApiKeyMissing,
			wantCode: plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER,
		},
		{
			name:     "invalid api key",
			headers:  requestHeaders("API-KEY", "wrong-key", "HASH-SIGNATURE", "signature"),
			wantHTTP: fiber.StatusUnauthorized,
			wantMsg:  config.ErrAuthS2SApiKeyInvalid,
			wantCode: plugnmeet.StatusCode_INVALID_API_KEY,
		},
		{
			name:     "missing signature",
			headers:  requestHeaders("API-KEY", authTestApiKey),
			wantHTTP: fiber.StatusBadRequest,
			wantMsg:  config.ErrAuthS2SSignatureMissing,
			wantCode: plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER,
		},
		{
			name:     "invalid signature",
			headers:  requestHeaders("API-KEY", authTestApiKey, "HASH-SIGNATURE", "deadbeef"),
			wantHTTP: fiber.StatusUnauthorized,
			wantMsg:  config.ErrAuthS2SSignatureInvalid,
			wantCode: plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE,
		},
		{
			name: "missing room id for multipart",
			headers: requestHeaders(
				"API-KEY", authTestApiKey,
				"HASH-SIGNATURE", "signature",
				fiber.HeaderContentType, fiber.MIMEMultipartForm,
			),
			wantHTTP: fiber.StatusBadRequest,
			wantMsg:  config.ErrAuthS2SRoomIdHeaderMissing,
			wantCode: plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := sendAuthTestRequest(t, app, http.MethodGet, "/auth/room/create", tt.headers)
			if resp.StatusCode != tt.wantHTTP {
				t.Fatalf("expected HTTP %d, got %d", tt.wantHTTP, resp.StatusCode)
			}
			res := decodeAuthJSONResponse(t, resp)
			if res.Status {
				t.Fatal("expected status false")
			}
			if res.Msg != tt.wantMsg.Error() {
				t.Fatalf("expected msg %q, got %q", tt.wantMsg.Error(), res.Msg)
			}
			if want := plugnmeet.StatusCode_name[int32(tt.wantCode)]; res.StatusCode != want {
				t.Fatalf("expected status_code %q, got %q", want, res.StatusCode)
			}
		})
	}
}

func TestHandleAuthHeaderCheckProtobufFailure(t *testing.T) {
	app := newAuthHeaderCheckApp()
	resp := sendAuthTestRequest(t, app, http.MethodGet, "/auth/room/create", requestHeaders(
		fiber.HeaderContentType, "application/protobuf",
		fiber.HeaderAccept, "application/protobuf",
	))
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected HTTP %d, got %d", fiber.StatusBadRequest, resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	res := new(plugnmeet.CommonResponse)
	if err := proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode protobuf response: %v", err)
	}
	if res.GetStatus() {
		t.Fatal("expected status false")
	}
	if res.GetMsg() != config.ErrAuthS2SApiKeyMissing.Error() {
		t.Fatalf("expected msg %q, got %q", config.ErrAuthS2SApiKeyMissing.Error(), res.GetMsg())
	}
	if res.GetStatusCode() != plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER {
		t.Fatalf("expected status code %s, got %s", plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER, res.GetStatusCode())
	}
}

func TestHandleVerifyHeaderTokenFailures(t *testing.T) {
	app := newVerifyTokenApp()
	const path = "/api/verifyToken"

	tests := []struct {
		name     string
		headers  map[string]string
		wantHTTP int
		wantKey  error
		wantCode plugnmeet.StatusCode
	}{
		{
			name:     "missing authorization header",
			headers:  nil,
			wantHTTP: fiber.StatusBadRequest,
			wantKey:  config.ErrAuthHeaderMissing,
			wantCode: plugnmeet.StatusCode_MISSING_REQUIRED_PARAMETER,
		},
		{
			name:     "invalid token",
			headers:  requestHeaders("Authorization", "invalid-token"),
			wantHTTP: fiber.StatusUnauthorized,
			wantKey:  config.ErrAuthTokenInvalid,
			wantCode: plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := sendAuthTestRequest(t, app, http.MethodPost, path, tt.headers)
			if resp.StatusCode != tt.wantHTTP {
				t.Fatalf("expected HTTP %d, got %d", tt.wantHTTP, resp.StatusCode)
			}
			res := decodeAuthJSONResponse(t, resp)
			if res.Msg != tt.wantKey.Error() {
				t.Fatalf("expected msg %q, got %q", tt.wantKey.Error(), res.Msg)
			}
			if want := plugnmeet.StatusCode_name[int32(tt.wantCode)]; res.StatusCode != want {
				t.Fatalf("expected status_code %q, got %q", want, res.StatusCode)
			}
		})
	}
}

func TestHandleVerifyHeaderTokenProtobufFailure(t *testing.T) {
	app := newVerifyTokenApp()
	resp := sendAuthTestRequest(t, app, http.MethodPost, "/api/verifyToken", requestHeaders(
		"Authorization", "invalid-token",
		fiber.HeaderContentType, "application/protobuf",
	))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expected HTTP %d, got %d", fiber.StatusUnauthorized, resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	res := new(plugnmeet.CommonResponse)
	if err := proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode protobuf response: %v", err)
	}
	if res.GetMsg() != config.ErrAuthTokenInvalid.Error() {
		t.Fatalf("expected msg %q, got %q", config.ErrAuthTokenInvalid.Error(), res.GetMsg())
	}
	if res.GetStatusCode() != plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE {
		t.Fatalf("expected status code %s, got %s", plugnmeet.StatusCode_INVALID_TOKEN_OR_SIGNATURE, res.GetStatusCode())
	}
}

// TestAuthErrorKeysAreTranslationKeys guards the contract: every browser facing
// auth message must be an i18n key, never English text.
func TestAuthErrorKeysAreTranslationKeys(t *testing.T) {
	sentinels := map[string]error{
		"unexpected":       config.ErrAuthUnexpected,
		"invalid request":  config.ErrAuthInvalidRequest,
		"auth header":      config.ErrAuthHeaderMissing,
		"token invalid":    config.ErrAuthTokenInvalid,
		"token expired":    config.ErrAuthTokenExpired,
		"room not active":  config.ErrAuthRoomNotActive,
		"user blocked":     config.ErrAuthUserBlocked,
		"user not active":  config.ErrAuthUserNotActive,
		"wait approval":    config.ErrAuthUserWaitApproval,
		"user info":        config.ErrAuthUserInfoNotFound,
		"duplicate entry":  config.ErrAuthDuplicateEntry,
		"user removed":     config.ErrAuthUserRemoved,
		"max participants": config.ErrAuthMaxParticipants,
	}
	for name, sentinel := range sentinels {
		msg := sentinel.Error()
		if strings.ContainsAny(msg, " \t") || !strings.Contains(msg, ".") {
			t.Errorf("%s: %q is not a translation key", name, msg)
		}
	}
	if strings.ContainsAny(config.AuthMsgTokenValid, " \t") || !strings.Contains(config.AuthMsgTokenValid, ".") {
		t.Errorf("token valid message %q is not a translation key", config.AuthMsgTokenValid)
	}
}

// TestAuthS2SMessagesArePlainEnglish guards the contract of the server-to-server
// /auth endpoints: curated English text, never an i18n key.
func TestAuthS2SMessagesArePlainEnglish(t *testing.T) {
	messages := map[string]error{
		"api key missing": config.ErrAuthS2SApiKeyMissing,
		"api key invalid": config.ErrAuthS2SApiKeyInvalid,
		"signature miss":  config.ErrAuthS2SSignatureMissing,
		"signature bad":   config.ErrAuthS2SSignatureInvalid,
		"room id header":  config.ErrAuthS2SRoomIdHeaderMissing,
	}
	for name, sentinel := range messages {
		msg := sentinel.Error()
		if msg == "" || strings.HasPrefix(msg, "notifications.") {
			t.Errorf("%s: %q must be a plain English message, not a translation key", name, msg)
		}
	}
}

// TestHandleVerifyTokenMalformedRequest covers an unparsable request body: it is
// an expected invalid request, reported with a translation key and not logged.
func TestHandleVerifyTokenMalformedRequest(t *testing.T) {
	logger, hook := logrusTest.NewNullLogger()
	ac := &AuthController{
		AppConfig: &config.AppConfig{Client: config.ClientInfo{ApiKey: authTestApiKey, Secret: authTestSecret}},
		Logger:    logger.WithField("controller", "auth"),
	}
	app := fiber.New()
	app.Post("/api/verifyToken", ac.HandleVerifyToken)

	req := httptest.NewRequest(http.MethodPost, "/api/verifyToken", strings.NewReader("not-a-protobuf"))
	req.Header.Set(fiber.HeaderContentType, "application/protobuf")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if resp.StatusCode != fiber.StatusBadRequest {
		t.Fatalf("expected HTTP %d, got %d", fiber.StatusBadRequest, resp.StatusCode)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	res := new(plugnmeet.CommonResponse)
	if err := proto.Unmarshal(body, res); err != nil {
		t.Fatalf("failed to decode protobuf response: %v", err)
	}
	if res.GetStatus() {
		t.Fatal("expected status false")
	}
	if res.GetMsg() != config.ErrAuthInvalidRequest.Error() {
		t.Fatalf("expected msg %q, got %q", config.ErrAuthInvalidRequest.Error(), res.GetMsg())
	}
	if res.GetStatusCode() != plugnmeet.StatusCode_INVALID_PARAMETERS {
		t.Fatalf("expected status code %s, got %s", plugnmeet.StatusCode_INVALID_PARAMETERS, res.GetStatusCode())
	}
	if entries := hook.AllEntries(); len(entries) != 0 {
		t.Fatalf("expected no log entries for an invalid request, got %d (%v)", len(entries), entries[0])
	}
}

// TestRoomAndUserMessagesAreTranslationKeys guards the client contract: room and
// user responses must carry i18n keys, never English text. The only plain-English
// server-to-server messages are the ErrAuthS2S* auth-header failures tested above.
func TestRoomAndUserMessagesAreTranslationKeys(t *testing.T) {
	sentinels := map[string]error{
		"room only admin":        config.ErrRoomOnlyAdmin,
		"room invalid request":   config.ErrRoomInvalidRequest,
		"room id required":       config.ErrRoomIdRequired,
		"room id mismatch":       config.ErrRoomIdMismatch,
		"room not found":         config.ErrRoomNotFound,
		"room not active":        config.ErrRoomNotActive,
		"room unexpected":        config.ErrRoomUnexpectedError,
		"room end failed":        config.ErrRoomEndFailed,
		"room id not allowed":    config.ErrRoomIdNotAllowed,
		"room preassigned limit": config.ErrRoomPreassignedLimit,
		"room preassigned user":  config.ErrRoomPreassignedUser,
		"user only admin":        config.ErrUserOnlyAdmin,
		"user invalid request":   config.ErrUserInvalidRequest,
		"user room not running":  config.ErrUserRoomNotRunning,
		"user not online":        config.ErrUserNotOnline,
		"user not found":         config.ErrUserNotFound,
		"user unexpected":        config.ErrUserUnexpectedError,
	}

	for name, sentinel := range sentinels {
		msg := sentinel.Error()
		if strings.ContainsAny(msg, " \t") || !strings.Contains(msg, ".") {
			t.Errorf("%s: %q is not a translation key", name, msg)
		}
	}
	if strings.ContainsAny(config.MsgSuccess, " \t") || !strings.Contains(config.MsgSuccess, ".") {
		t.Errorf("success message %q is not a translation key", config.MsgSuccess)
	}
}
