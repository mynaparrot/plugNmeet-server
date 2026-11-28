package avatar

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

const (
	// heyGenMode runs the avatar without its own LLM: the agent owns the speech,
	// so the avatar only renders the lip-sync of the streamed audio. LITE is the
	// mode livekit-plugins-liveavatar sends for the same setup.
	heyGenMode = "LITE"
	// heyGenStopReason is reported to HeyGen when the agent ends a session.
	heyGenStopReason = "USER_DISCONNECTED"
)

// heyGenLiveKitConfig points the avatar at the room of the agent.
type heyGenLiveKitConfig struct {
	LiveKitRoom        string `json:"livekit_room,omitempty"`
	LiveKitURL         string `json:"livekit_url"`
	LiveKitClientToken string `json:"livekit_client_token"`
}

type heyGenTokenRequest struct {
	Mode          string              `json:"mode"`
	AvatarID      string              `json:"avatar_id"`
	LiveKitConfig heyGenLiveKitConfig `json:"livekit_config"`
}

// heyGenSession is the session data of the token and start responses. The API
// wraps it in "data"; a flat response is read as well.
type heyGenSession struct {
	SessionID    string         `json:"session_id"`
	SessionToken string         `json:"session_token"`
	WSURL        string         `json:"ws_url"`
	Data         *heyGenSession `json:"data"`
}

// pick returns the session data, whatever way the API wrapped it.
func (r *heyGenSession) pick() *heyGenSession {
	if r.Data != nil {
		return r.Data
	}

	return r
}

func startHeyGen(ctx context.Context, client *http.Client, cfg StartConfig, baseURL string) (Session, error) {
	tokenRequest := heyGenTokenRequest{
		Mode:     heyGenMode,
		AvatarID: cfg.AvatarID,
		LiveKitConfig: heyGenLiveKitConfig{
			LiveKitRoom:        cfg.LiveKitRoom,
			LiveKitURL:         cfg.LiveKitURL,
			LiveKitClientToken: cfg.LiveKitToken,
		},
	}

	var tokenResponse heyGenSession
	if err := postJSON(ctx, client, baseURL+"/token", apiKeyHeaders(cfg, "X-API-KEY", false), tokenRequest, &tokenResponse); err != nil {
		return nil, fmt.Errorf("heygen: failed to create session token: %w", err)
	}
	token := tokenResponse.pick()
	if token.SessionID == "" || token.SessionToken == "" {
		return nil, fmt.Errorf("heygen: token response is missing session_id or session_token")
	}

	// The session token is the credential of this one session, so it replaces
	// the API key from here on.
	sessionHeaders := map[string]string{"Authorization": "Bearer " + token.SessionToken}
	var startResponse heyGenSession
	if err := postJSON(ctx, client, baseURL+"/start", sessionHeaders, map[string]any{"session_id": token.SessionID}, &startResponse); err != nil {
		return nil, fmt.Errorf("heygen: failed to start session: %w", err)
	}
	started := startResponse.pick()
	if started.WSURL == "" {
		return nil, fmt.Errorf("heygen: start response is missing ws_url")
	}

	sessionID := started.SessionID
	if sessionID == "" {
		sessionID = token.SessionID
	}

	return newWSSession(ctx, wsSessionConfig{
		provider:      config.ProviderHeyGen,
		sessionID:     sessionID,
		sampleRate:    media.DefaultOutputSampleRate24k,
		wsURL:         started.WSURL,
		waitConnected: true,
		dialContext:   cfg.DialContext,
		closeFn: func(ctx context.Context) error {
			payload := map[string]any{"session_id": sessionID, "reason": heyGenStopReason}
			if err := postJSON(ctx, client, baseURL+"/stop", sessionHeaders, payload, nil); err != nil {
				return fmt.Errorf("heygen: failed to stop session: %w", err)
			}
			return nil
		},
	})
}
