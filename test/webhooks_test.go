package test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"github.com/goccy/go-json"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/handler"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func test_webhooks(t *testing.T, rInfo *livekit.Room, roomFinished bool) {
	body := &livekit.WebhookEvent{
		Room: rInfo,
		Participant: &livekit.ParticipantInfo{
			Name:     "Test",
			Identity: "test001",
		},
	}

	tests := []struct {
		event string
	}{
		{
			event: "room_started",
		},
		{
			event: "participant_joined",
		},
		{
			event: "participant_left",
		},
	}

	if roomFinished {
		tests = []struct {
			event string
		}{
			{
				event: "room_finished",
			},
		}
	}

	for _, tt := range tests {
		body.Event = tt.event

		var encoded []byte
		var err error

		encoded, err = json.Marshal(body)
		if err != nil {
			t.Error(err)
		}
		// sign payload
		sum := sha256.Sum256(encoded)
		b64 := base64.StdEncoding.EncodeToString(sum[:])

		at := auth.NewAccessToken(config.AppCnf.LivekitInfo.ApiKey, config.AppCnf.LivekitInfo.Secret).
			SetValidFor(5 * time.Minute).
			SetSha256(b64)
		token, err := at.ToJWT()

		if err != nil {
			t.Error(err)
		}

		t.Run("Webhook_"+body.Event, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(encoded))
			req.Header.Set("Authorization", token)
			req.Header.Set("Content-Type", "application/json")

			router := handler.Router()
			res, err := router.Test(req)
			if err != nil {
				t.Error(err)
			}
			if res.StatusCode != 200 {
				t.Errorf("Error in router: %s, Error code: %d", "/webhook", res.StatusCode)
			}
		})

	}
}
