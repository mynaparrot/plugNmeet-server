package avatar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

func TestStartSessionTavus(t *testing.T) {
	var gotPath, gotAPIKey string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations":
			gotPath = r.URL.Path
			gotAPIKey = r.Header.Get("x-api-key")
			gotBody = decodeBody(t, r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-1"}`))
		case "/conversations/tavus-conversation-1/end":
			// Closing must reach the end endpoint of the conversation.
			gotPath = r.URL.Path
		default:
			t.Errorf("unexpected path: %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderTavus, srv.URL))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if gotPath != "/conversations" {
		t.Errorf("unexpected create path: %q", gotPath)
	}
	if gotAPIKey != testAPIKey {
		t.Errorf("unexpected x-api-key header: %q", gotAPIKey)
	}
	// A bare avatar id is a face that runs on the default PAL.
	if gotBody["pal_id"] != defaultTavusPalID {
		t.Errorf("body[pal_id] = %v, want %v", gotBody["pal_id"], defaultTavusPalID)
	}
	if gotBody["face_id"] != testAvatarID {
		t.Errorf("body[face_id] = %v, want %v", gotBody["face_id"], testAvatarID)
	}
	props, ok := gotBody["properties"].(map[string]any)
	if !ok {
		t.Fatalf("body[properties] is not an object: %v", gotBody["properties"])
	}
	if props["livekit_ws_url"] != testLiveKitURL {
		t.Errorf("properties[livekit_ws_url] = %v, want %v", props["livekit_ws_url"], testLiveKitURL)
	}
	if props["livekit_room_token"] != testLiveKitToken {
		t.Errorf("properties[livekit_room_token] = %v, want %v", props["livekit_room_token"], testLiveKitToken)
	}

	if sess.Provider() != config.ProviderTavus {
		t.Errorf("Provider() = %q", sess.Provider())
	}
	if sess.SessionID() != "tavus-conversation-1" {
		t.Errorf("SessionID() = %q", sess.SessionID())
	}
	if sess.SampleRate() != media.DefaultOutputSampleRate24k {
		t.Errorf("SampleRate() = %d, want %d", sess.SampleRate(), media.DefaultOutputSampleRate24k)
	}

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
	if gotPath != "/conversations/tavus-conversation-1/end" {
		t.Errorf("close path = %q, want the end endpoint of the conversation", gotPath)
	}
}

func TestStartSessionTavusFaceAndPal(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-2"}`))
	}))
	defer srv.Close()

	cfg := newTestConfig(config.ProviderTavus, srv.URL)
	cfg.AvatarID = "face-1/pal-1"
	if _, err := StartSession(context.Background(), cfg); err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if gotBody["face_id"] != "face-1" || gotBody["pal_id"] != "pal-1" {
		t.Errorf("body = %v, want face-1 on pal-1", gotBody)
	}
}

// A leading slash selects the default face of the PAL, so no face_id is sent.
func TestStartSessionTavusPalOnly(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-3"}`))
	}))
	defer srv.Close()

	cfg := newTestConfig(config.ProviderTavus, srv.URL)
	cfg.AvatarID = "/pal-1"
	if _, err := StartSession(context.Background(), cfg); err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if _, ok := gotBody["face_id"]; ok {
		t.Errorf("body = %v, want no face_id", gotBody)
	}
	if gotBody["pal_id"] != "pal-1" {
		t.Errorf("body[pal_id] = %v, want pal-1", gotBody["pal_id"])
	}
}

func TestStartSessionTavusNormalizesLiveKitURL(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-4"}`))
	}))
	defer srv.Close()

	cfg := newTestConfig(config.ProviderTavus, srv.URL)
	cfg.LiveKitURL = "https://livekit.example.com/"
	if _, err := StartSession(context.Background(), cfg); err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	props, ok := gotBody["properties"].(map[string]any)
	if !ok {
		t.Fatalf("body[properties] is not an object: %v", gotBody["properties"])
	}
	if props["livekit_ws_url"] != "wss://livekit.example.com" {
		t.Errorf("properties[livekit_ws_url] = %v", props["livekit_ws_url"])
	}
}

func TestStartSessionTavusCloseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"conversation_id":"tavus-conversation-5"}`))
	}))
	defer srv.Close()

	sess, err := StartSession(context.Background(), newTestConfig(config.ProviderTavus, srv.URL))
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}
	if err := sess.Close(context.Background()); err == nil {
		t.Fatal("expected an error from the failing end endpoint")
	}
}

func TestParseTavusAvatarID(t *testing.T) {
	tests := []struct {
		raw       string
		wantFace  string
		wantPalID string
	}{
		{"face-1", "face-1", defaultTavusPalID},
		{"  face-1  ", "face-1", defaultTavusPalID},
		{"face-1/pal-1", "face-1", "pal-1"},
		{"face-1 / pal-1 ", "face-1", "pal-1"},
		{"/pal-1", "", "pal-1"},
		{"face-1/", "face-1", defaultTavusPalID},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			face, pal := parseTavusAvatarID(tt.raw)
			if face != tt.wantFace || pal != tt.wantPalID {
				t.Errorf("parseTavusAvatarID(%q) = %q/%q, want %q/%q", tt.raw, face, pal, tt.wantFace, tt.wantPalID)
			}
		})
	}
}

func TestTavusEmotionPayload(t *testing.T) {
	payload, err := TavusEmotionPayload(TavusEmotionHappiness)
	if err != nil {
		t.Fatalf("TavusEmotionPayload returned error: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("failed to decode the payload: %v", err)
	}
	if got["message_type"] != "conversation" || got["event_type"] != "conversation.set_emotion" {
		t.Errorf("unexpected payload: %s", payload)
	}
	props, ok := got["properties"].(map[string]any)
	if !ok || props["emotion"] != TavusEmotionHappiness {
		t.Errorf("unexpected properties: %s", payload)
	}

	if _, err := TavusEmotionPayload("anger"); err == nil {
		t.Fatal("expected an error for an unsupported emotion")
	}
	if _, err := TavusEmotionPayload(" "); err == nil {
		t.Fatal("expected an error for an empty emotion")
	}
}
