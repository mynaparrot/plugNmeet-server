// Package avatar starts realtime avatar sessions with third-party providers.
//
// An avatar provider joins the LiveKit room of the AI agent as a participant
// and publishes the lip-synced video of the configured avatar. Starting a
// session is therefore a single provider API call that carries the room URL and
// the participant token the agent pre-created; the provider owns the media
// plane from that point on.
package avatar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

const (
	// defaultBeyondPresenceEndpoint is the Beyond Presence API base URL.
	defaultBeyondPresenceEndpoint = "https://api.bey.dev"
	// defaultAnamEndpoint is the Anam AI API base URL.
	defaultAnamEndpoint = "https://api.anam.ai"
	// defaultSimliEndpoint is the Simli API base URL.
	defaultSimliEndpoint = "https://api.simli.ai"

	// defaultHTTPTimeout bounds a single provider API call.
	defaultHTTPTimeout = 15 * time.Second
	// responseBodyLimit caps how much of a response body is read. Provider
	// session responses are small, so this only guards against misuse.
	responseBodyLimit = 64 * 1024
	// errorBodyLimit is the number of response body bytes kept in error messages.
	errorBodyLimit = 256
)

// StartConfig holds everything needed to start one avatar session.
type StartConfig struct {
	Provider config.ProviderType
	APIKey   string
	AvatarID string
	Endpoint string // optional base URL override for self-hosted/testing
	// AuthHeader is the optional header name that carries the API key, e.g. for
	// a custom gateway. Empty keeps the provider's own default header.
	AuthHeader   string
	LiveKitURL   string
	LiveKitToken string
	HTTPClient   *http.Client // optional; defaults to 15s timeout client
}

// Session is a started avatar session.
type Session interface {
	Provider() config.ProviderType
	SessionID() string
	SampleRate() int
	Close(ctx context.Context) error
}

// StartSession creates a provider session and returns its handle. The provider
// bot is expected to join cfg.LiveKitURL with cfg.LiveKitToken.
func StartSession(ctx context.Context, cfg StartConfig) (Session, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("avatar: api key is required")
	}
	if cfg.AvatarID == "" {
		return nil, fmt.Errorf("avatar: avatar id is required")
	}
	if cfg.LiveKitURL == "" {
		return nil, fmt.Errorf("avatar: livekit url is required")
	}
	if cfg.LiveKitToken == "" {
		return nil, fmt.Errorf("avatar: livekit token is required")
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	switch cfg.Provider {
	case config.ProviderBeyondPresence:
		return startBeyondPresence(ctx, client, cfg, endpointOrDefault(cfg.Endpoint, defaultBeyondPresenceEndpoint))
	case config.ProviderAnam:
		return startAnam(ctx, client, cfg, endpointOrDefault(cfg.Endpoint, defaultAnamEndpoint))
	case config.ProviderSimli:
		return startSimli(ctx, client, cfg, endpointOrDefault(cfg.Endpoint, defaultSimliEndpoint))
	default:
		return nil, fmt.Errorf("avatar: unsupported provider %q", cfg.Provider)
	}
}

// endpointOrDefault trims the trailing slash of an endpoint override and falls
// back to the provider default when no override is set.
func endpointOrDefault(endpoint, fallback string) string {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return fallback
	}
	return endpoint
}

// apiKeyHeaders builds the header that carries the API key. A custom
// cfg.AuthHeader replaces the provider's default header name, so a gateway that
// expects another header can be used; without one the provider default stays.
// bearerDefault reports that the provider's documented default is a bearer
// Authorization header.
func apiKeyHeaders(cfg StartConfig, defaultHeader string, bearerDefault bool) map[string]string {
	if header := strings.TrimSpace(cfg.AuthHeader); header != "" {
		return map[string]string{header: apiKeyHeaderValue(header, cfg.APIKey)}
	}
	if bearerDefault {
		return map[string]string{defaultHeader: "Bearer " + cfg.APIKey}
	}

	return map[string]string{defaultHeader: cfg.APIKey}
}

// apiKeyHeaderValue returns the value of a custom header: an Authorization
// header is a bearer credential, so the scheme is added unless the key already
// carries one. Every other header gets the bare key.
func apiKeyHeaderValue(header, apiKey string) string {
	if strings.EqualFold(header, "Authorization") && !strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
		return "Bearer " + apiKey
	}

	return apiKey
}

// session is the shared implementation of Session for every provider.
type session struct {
	provider   config.ProviderType
	sessionID  string
	sampleRate int
}

func (s *session) Provider() config.ProviderType { return s.provider }
func (s *session) SessionID() string             { return s.sessionID }
func (s *session) SampleRate() int               { return s.sampleRate }

// Close is a no-op: every supported provider terminates the avatar bot when the
// bot participant is removed from the LiveKit room, so closing the room is
// enough and there is no session endpoint to call.
func (s *session) Close(ctx context.Context) error { return nil }

// beyondPresenceStartResponse is the subset of the session create response used.
type beyondPresenceStartResponse struct {
	ID string `json:"id"`
}

func startBeyondPresence(ctx context.Context, client *http.Client, cfg StartConfig, endpoint string) (Session, error) {
	payload := map[string]any{
		"avatar_id":      cfg.AvatarID,
		"transport_type": "livekit",
		"livekit_url":    cfg.LiveKitURL,
		"livekit_token":  cfg.LiveKitToken,
	}
	headers := apiKeyHeaders(cfg, "x-api-key", false)

	var resp beyondPresenceStartResponse
	if err := postJSON(ctx, client, endpoint+"/v1/sessions", headers, payload, &resp); err != nil {
		return nil, fmt.Errorf("beyond_presence: failed to start session: %w", err)
	}
	if resp.ID == "" {
		return nil, fmt.Errorf("beyond_presence: session response is missing id")
	}

	return &session{
		provider:   config.ProviderBeyondPresence,
		sessionID:  resp.ID,
		sampleRate: media.DefaultSampleRate16k,
	}, nil
}

// anamPersonaConfig is the ephemeral persona Anam starts for a session.
type anamPersonaConfig struct {
	Type     string `json:"type"`
	AvatarID string `json:"avatarId"`
	LLMID    string `json:"llmId"`
}

// anamEnvironment points Anam at the LiveKit room of the agent.
type anamEnvironment struct {
	LiveKitURL   string `json:"livekitUrl"`
	LiveKitToken string `json:"livekitToken"`
}

type anamStartRequest struct {
	PersonaConfig anamPersonaConfig `json:"personaConfig"`
	Environment   anamEnvironment   `json:"environment"`
}

type anamStartResponse struct {
	SessionID string `json:"sessionId"`
}

func startAnam(ctx context.Context, client *http.Client, cfg StartConfig, endpoint string) (Session, error) {
	payload := anamStartRequest{
		PersonaConfig: anamPersonaConfig{
			Type:     "ephemeral",
			AvatarID: cfg.AvatarID,
			// The avatar only speaks the agent audio, so Anam runs without an LLM.
			LLMID: "CUSTOMER_CLIENT_V1",
		},
		Environment: anamEnvironment{
			LiveKitURL:   cfg.LiveKitURL,
			LiveKitToken: cfg.LiveKitToken,
		},
	}
	headers := apiKeyHeaders(cfg, "Authorization", true)

	var resp anamStartResponse
	if err := postJSON(ctx, client, endpoint+"/v1/engine/session", headers, payload, &resp); err != nil {
		return nil, fmt.Errorf("anam: failed to start session: %w", err)
	}
	if resp.SessionID == "" {
		return nil, fmt.Errorf("anam: session response is missing sessionId")
	}

	return &session{
		provider:   config.ProviderAnam,
		sessionID:  resp.SessionID,
		sampleRate: media.DefaultOutputSampleRate24k,
	}, nil
}

// simliTokenRequest asks Simli for a short lived session token for the face.
type simliTokenRequest struct {
	FaceID           string `json:"faceId"`
	HandleSilence    bool   `json:"handleSilence"`
	MaxSessionLength int    `json:"maxSessionLength"`
	MaxIdleTime      int    `json:"maxIdleTime"`
}

type simliTokenResponse struct {
	SessionToken string `json:"session_token"`
}

// simliLiveKitRequest asks Simli to join the LiveKit room with the token.
type simliLiveKitRequest struct {
	SessionToken string `json:"session_token"`
	LiveKitURL   string `json:"livekit_url"`
	LiveKitToken string `json:"livekit_token"`
}

type simliLiveKitResponse struct {
	SessionID string `json:"session_id"`
}

func startSimli(ctx context.Context, client *http.Client, cfg StartConfig, endpoint string) (Session, error) {
	headers := apiKeyHeaders(cfg, "x-simli-api-key", false)

	tokenReq := simliTokenRequest{
		FaceID:           cfg.AvatarID,
		HandleSilence:    true,
		MaxSessionLength: 3600,
		MaxIdleTime:      300,
	}
	var tokenResp simliTokenResponse
	if err := postJSON(ctx, client, endpoint+"/compose/token", headers, tokenReq, &tokenResp); err != nil {
		return nil, fmt.Errorf("simli: failed to create session token: %w", err)
	}
	if tokenResp.SessionToken == "" {
		return nil, fmt.Errorf("simli: token response is missing session_token")
	}

	joinReq := simliLiveKitRequest{
		SessionToken: tokenResp.SessionToken,
		LiveKitURL:   cfg.LiveKitURL,
		LiveKitToken: cfg.LiveKitToken,
	}
	var joinResp simliLiveKitResponse
	if err := postJSON(ctx, client, endpoint+"/integrations/livekit/agents", headers, joinReq, &joinResp); err != nil {
		return nil, fmt.Errorf("simli: failed to join livekit room: %w", err)
	}

	sessionID := joinResp.SessionID
	if sessionID == "" {
		sessionID = tokenResp.SessionToken
	}

	return &session{
		provider:   config.ProviderSimli,
		sessionID:  sessionID,
		sampleRate: media.DefaultSampleRate16k,
	}, nil
}

// postJSON sends payload as JSON and decodes a 2xx response into out (when out
// is non-nil). A non-2xx response is reported with its status code and a
// truncated body to keep troubleshooting cheap.
func postJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, payload, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, responseBodyLimit))
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet := respBody
		if len(snippet) > errorBodyLimit {
			snippet = snippet[:errorBodyLimit]
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("failed to decode response body: %w", err)
	}

	return nil
}
