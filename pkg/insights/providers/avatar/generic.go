package avatar

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

// genericSessionRequest starts a session on a self-hosted avatar server that
// joins the LiveKit room of the agent.
type genericSessionRequest struct {
	AvatarID     string `json:"avatar_id"`
	LiveKitURL   string `json:"livekit_url"`
	LiveKitToken string `json:"livekit_token"`
	LiveKitRoom  string `json:"livekit_room,omitempty"`
	SampleRate   int    `json:"sample_rate"`
}

// genericSessionResponse is everything a generic avatar server may answer with.
// Every field is optional so any compatible server works; the server may wrap
// the data in "data".
type genericSessionResponse struct {
	SessionID      string                  `json:"session_id"`
	ID             string                  `json:"id"`
	ConversationID string                  `json:"conversation_id"`
	TaskID         string                  `json:"task_id"`
	SampleRate     int                     `json:"sample_rate"`
	EndURL         string                  `json:"end_url"`
	WSURL          string                  `json:"ws_url"`
	Data           *genericSessionResponse `json:"data"`
}

// pick returns the session data, whatever way the server wrapped it.
func (r *genericSessionResponse) pick() *genericSessionResponse {
	if r.Data != nil {
		return r.Data
	}

	return r
}

// startGenericDataStream starts a session on a self-hosted LiveKit avatar worker
// (TruGen or any other AvatarRunner). The worker renders the avatar into the
// room of the agent and reads the voice from the lk.audio_stream datastream, so
// the response carries no ws_url and the agent needs no socket.
func startGenericDataStream(ctx context.Context, client *http.Client, cfg StartConfig) (Session, error) {
	return startGenericRest(ctx, client, cfg, config.ProviderLiveKitDataStream)
}

// startGenericWebSocket starts a websocket avatar session. A socket endpoint
// takes the session config as its first message; an http endpoint is called
// first and answers with the socket to use.
func startGenericWebSocket(ctx context.Context, client *http.Client, cfg StartConfig) (Session, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("%s: endpoint is required", config.ProviderWebSocket)
	}

	lower := strings.ToLower(endpoint)
	if strings.HasPrefix(lower, "wss://") || strings.HasPrefix(lower, "ws://") {
		return newWSSession(ctx, wsSessionConfig{
			provider:    config.ProviderWebSocket,
			sessionID:   cfg.AvatarID,
			sampleRate:  media.DefaultOutputSampleRate24k,
			wsURL:       endpoint,
			headers:     apiKeyHeaders(cfg, "Authorization", true),
			initPayload: genericInitMessage(cfg),
			dialContext: cfg.DialContext,
		})
	}

	return startGenericRest(ctx, client, cfg, config.ProviderWebSocket)
}

// genericInitMessage is the session config a direct socket endpoint receives.
func genericInitMessage(cfg StartConfig) map[string]any {
	message := map[string]any{
		"type":          "session.init",
		"avatar_id":     cfg.AvatarID,
		"livekit_url":   cfg.LiveKitURL,
		"livekit_token": cfg.LiveKitToken,
		"sample_rate":   media.DefaultOutputSampleRate24k,
	}
	if cfg.LiveKitRoom != "" {
		message["livekit_room"] = cfg.LiveKitRoom
	}

	return message
}

// startGenericRest boots a generic session over REST. A websocket provider
// answers with the socket the agent must dial; a datastream provider renders the
// avatar in the room and takes the voice over lk.audio_stream, so its session
// stays socket-free.
func startGenericRest(ctx context.Context, client *http.Client, cfg StartConfig, provider config.ProviderType) (Session, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("%s: endpoint is required", provider)
	}

	headers := apiKeyHeaders(cfg, "Authorization", true)
	request := genericSessionRequest{
		AvatarID:     cfg.AvatarID,
		LiveKitURL:   cfg.LiveKitURL,
		LiveKitToken: cfg.LiveKitToken,
		LiveKitRoom:  cfg.LiveKitRoom,
		SampleRate:   media.DefaultOutputSampleRate24k,
	}

	var response genericSessionResponse
	if err := postJSON(ctx, client, endpoint, headers, request, &response); err != nil {
		return nil, fmt.Errorf("%s: failed to start session: %w", provider, err)
	}

	started := response.pick()
	sessionID := genericSessionID(started)

	var closeFn func(ctx context.Context) error
	if endURL := strings.TrimSpace(started.EndURL); endURL != "" {
		closeFn = func(ctx context.Context) error {
			if err := postJSON(ctx, client, endURL, headers, map[string]any{"session_id": sessionID}, nil); err != nil {
				return fmt.Errorf("%s: failed to end session: %w", provider, err)
			}
			return nil
		}
	}

	// Without a socket the session has no AudioPublisher, so the agent streams the
	// voice to the avatar participant over the LiveKit datastream.
	if provider != config.ProviderWebSocket {
		return &session{
			provider:   provider,
			sessionID:  sessionID,
			sampleRate: genericSessionSampleRate(started),
			closeFn:    closeFn,
		}, nil
	}

	if started.WSURL == "" {
		return nil, fmt.Errorf("%s: session response is missing ws_url", provider)
	}

	return newWSSession(ctx, wsSessionConfig{
		provider:      provider,
		sessionID:     sessionID,
		sampleRate:    genericSessionSampleRate(started),
		wsURL:         started.WSURL,
		waitConnected: true,
		closeFn:       closeFn,
		dialContext:   cfg.DialContext,
	})
}

// genericSessionID names the session with whatever the server called it.
func genericSessionID(resp *genericSessionResponse) string {
	for _, candidate := range []string{resp.SessionID, resp.ID, resp.ConversationID, resp.TaskID} {
		if id := strings.TrimSpace(candidate); id != "" {
			return id
		}
	}

	return "datastream"
}

// genericSessionSampleRate reads the rate the server wants the audio at.
func genericSessionSampleRate(resp *genericSessionResponse) int {
	if resp.SampleRate == media.DefaultSampleRate16k {
		return media.DefaultSampleRate16k
	}

	return media.DefaultOutputSampleRate24k
}
