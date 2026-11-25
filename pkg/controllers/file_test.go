package controllers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestAuthApiFileMessagesArePlainEnglish guards the integrator contract: the Auth API
// file endpoint answers with curated English text, never with the client i18n keys
// (room.errors.*, file.errors.*) that browsers resolve via t().
func TestAuthApiFileMessagesArePlainEnglish(t *testing.T) {
	messages := map[string]string{
		"room id required":      config.MsgRoomIdRequired,
		"invalid request":       config.MsgFileInvalidRequest,
		"upload in progress":    config.MsgFileUploadInProgress,
		"invalid document link": config.MsgFileInvalidDocumentLink,
		"too large":             config.MsgFileTooLarge,
		"type not allowed":      config.MsgFileTypeNotAllowed,
		"upload failed":         config.MsgFileUploadFailed,
		"conversion failed":     config.MsgFileConversionFailed,
	}

	for name, msg := range messages {
		if !strings.Contains(msg, " ") {
			t.Errorf("%s: %q is not plain English", name, msg)
		}
		if strings.Contains(msg, "errors.") || strings.HasPrefix(msg, "notifications.") {
			t.Errorf("%s: %q looks like an i18n key", name, msg)
		}
	}

	// the room model's messages are client i18n keys and must never reach integrators
	for _, msg := range messages {
		for _, key := range []error{config.ErrRoomNotActive, config.ErrRoomNotFound, config.ErrRoomUnexpectedError} {
			if msg == key.Error() {
				t.Errorf("room i18n key leaked into the Auth API file message: %q", msg)
			}
		}
	}
}

// TestUploadWhiteboardFileCallerMessages proves the /auth file endpoint answers an
// integrator with curated English plus the i18n key in msg_key, and a browser
// client with the key alone (no msg_key).
func TestUploadWhiteboardFileCallerMessages(t *testing.T) {
	fc := &FileController{}

	tests := []struct {
		name         string
		asIntegrator bool
		wantMsg      string
		wantKey      string
	}{
		{name: "client", wantMsg: config.ErrRoomIdRequired.Error()},
		{
			name:         "integrator",
			asIntegrator: true,
			wantMsg:      config.MsgRoomIdRequired,
			wantKey:      config.ErrRoomIdRequired.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newCallerTestApp(fc.HandleUploadWhiteboardFile, tt.asIntegrator)
			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/test", nil))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("http status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}
			res := new(plugnmeet.CommonResponse)
			if err = protojson.Unmarshal(body, res); err != nil {
				t.Fatalf("failed to decode CommonResponse: %v", err)
			}
			if res.GetMsg() != tt.wantMsg {
				t.Fatalf("msg = %q, want %q", res.GetMsg(), tt.wantMsg)
			}
			if res.GetMsgKey() != tt.wantKey {
				t.Fatalf("msg_key = %q, want %q", res.GetMsgKey(), tt.wantKey)
			}
			if tt.wantKey == "" && res.MsgKey != nil {
				t.Fatalf("msg_key should be unset, got %q", res.GetMsgKey())
			}
		})
	}
}
