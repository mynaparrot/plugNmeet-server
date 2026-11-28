package avatar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

const (
	// defaultTavusPalID is the PAL a bare face id runs on.
	defaultTavusPalID = "pb87e71797da"
	// tavusConversationName labels the conversations started by the agent.
	tavusConversationName = "plugnmeet"
	// TavusAppMessagesTopic is the LiveKit data topic the Tavus bot reads its
	// app messages (emotion, interrupt) from.
	TavusAppMessagesTopic = "app_messages"

	// TavusEmotionNeutral keeps the avatar calm while it is not speaking.
	TavusEmotionNeutral = "neutral"
	// TavusEmotionHappiness makes the avatar smile while it speaks.
	TavusEmotionHappiness = "happiness"
)

// tavusEmotions is the set of emotions the agent drives the avatar with.
var tavusEmotions = map[string]struct{}{
	TavusEmotionNeutral:   {},
	TavusEmotionHappiness: {},
}

// TavusEmotionPayload builds the app message that sets the emotion of the
// avatar.
func TavusEmotionPayload(emotion string) ([]byte, error) {
	emotion = strings.TrimSpace(emotion)
	if _, ok := tavusEmotions[emotion]; !ok {
		return nil, fmt.Errorf("tavus: unsupported emotion %q", emotion)
	}

	return json.Marshal(map[string]any{
		"message_type": "conversation",
		"event_type":   "conversation.set_emotion",
		"properties":   map[string]string{"emotion": emotion},
	})
}

// parseTavusAvatarID splits the configured avatar id into the face and the PAL
// of a conversation. "<face_id>" runs that face on the default PAL,
// "<face_id>/<pal_id>" combines both, and "/<pal_id>" uses the default face of
// the PAL.
func parseTavusAvatarID(raw string) (faceID, palID string) {
	faceID, palID, hasPal := strings.Cut(strings.TrimSpace(raw), "/")
	faceID, palID = strings.TrimSpace(faceID), strings.TrimSpace(palID)
	if !hasPal || palID == "" {
		return faceID, defaultTavusPalID
	}

	return faceID, palID
}

type tavusLiveKitProperties struct {
	LiveKitWSURL     string `json:"livekit_ws_url"`
	LiveKitRoomToken string `json:"livekit_room_token"`
}

type tavusCreateConversationRequest struct {
	PalID            string                 `json:"pal_id"`
	FaceID           string                 `json:"face_id,omitempty"`
	ConversationName string                 `json:"conversation_name"`
	Properties       tavusLiveKitProperties `json:"properties"`
}

type tavusCreateConversationResponse struct {
	ConversationID string `json:"conversation_id"`
}

func startTavus(ctx context.Context, client *http.Client, cfg StartConfig, baseURL string) (Session, error) {
	faceID, palID := parseTavusAvatarID(cfg.AvatarID)
	payload := tavusCreateConversationRequest{
		PalID:            palID,
		FaceID:           faceID,
		ConversationName: tavusConversationName,
		Properties: tavusLiveKitProperties{
			LiveKitWSURL:     cfg.LiveKitURL,
			LiveKitRoomToken: cfg.LiveKitToken,
		},
	}
	headers := apiKeyHeaders(cfg, "x-api-key", false)

	var resp tavusCreateConversationResponse
	if err := postJSON(ctx, client, baseURL+"/conversations", headers, payload, &resp); err != nil {
		return nil, fmt.Errorf("tavus: failed to start conversation: %w", err)
	}
	if resp.ConversationID == "" {
		return nil, fmt.Errorf("tavus: conversation response is missing conversation_id")
	}

	conversationID := resp.ConversationID
	endURL := baseURL + "/conversations/" + url.PathEscape(conversationID) + "/end"

	return &session{
		provider:   config.ProviderTavus,
		sessionID:  conversationID,
		sampleRate: media.DefaultOutputSampleRate24k,
		closeFn: func(ctx context.Context) error {
			if err := postJSON(ctx, client, endURL, headers, map[string]any{}, nil); err != nil {
				return fmt.Errorf("tavus: failed to end conversation: %w", err)
			}
			return nil
		},
	}, nil
}
