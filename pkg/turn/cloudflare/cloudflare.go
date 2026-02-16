package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn"
)

const (
	generateAPIEndpoint = "https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/generate-ice-servers"
	revokeAPIEndpoint   = "https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/%s/revoke"
	defaultTTL          = 86400 // 24 hours
)

var (
	cloudflareURIs = []string{
		"turn:turn.cloudflare.com:3478?transport=udp",
		"turns:turn.cloudflare.com:443?transport=tcp",
	}
)

// iceServer defines the structure for a single server in the Cloudflare response.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`   // omitempty because STUN servers don't have it
	Credential string   `json:"credential,omitempty"` // omitempty because STUN servers don't have it
}

// cloudflareResponse defines the structure of the top-level JSON response.
type cloudflareResponse struct {
	ICEServers []iceServer `json:"iceServers"`
}

// CloudflareProvider implements the turn.Provider interface for Cloudflare.
type CloudflareProvider struct {
	client *http.Client
}

func NewCloudflareProvider() turn.Provider {
	return &CloudflareProvider{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// GetTURNServerCredentials fetches temporary TURN credentials from the Cloudflare API.
func (p *CloudflareProvider) GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider, roomId, userId string) (*turn.Credentials, error) {
	apiKey, ok := c.Options["key_api_token"].(string)
	if !ok || apiKey == "" {
		return nil, fmt.Errorf("cloudflare key_api_token is not configured in options")
	}

	keyID, ok := c.Options["key_id"].(string)
	if !ok || keyID == "" {
		return nil, fmt.Errorf("cloudflare key_id is not configured in options")
	}

	ttl := defaultTTL
	if configTTL, ok := c.Options["ttl"].(int); ok {
		ttl = configTTL
	}

	payloadStruct := struct {
		TTL              int    `json:"ttl"`
		CustomIdentifier string `json:"customIdentifier,omitempty"`
	}{
		TTL:              ttl,
		CustomIdentifier: fmt.Sprintf("%s", roomId),
	}
	payload, err := json.Marshal(payloadStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cloudflare ttl payload: %w", err)
	}

	url := fmt.Sprintf(generateAPIEndpoint, keyID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create cloudflare http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute cloudflare request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("cloudflare API request failed with status %d", resp.StatusCode)
	}

	var cfResp cloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("failed to decode cloudflare response: %w", err)
	}

	// Prepare the credentials with the static URIs and the requested TTL.
	creds := &turn.Credentials{
		URIs: cloudflareURIs,
		TTL:  ttl,
	}

	// Find the TURN server entry in the response array and populate the credentials.
	for _, server := range cfResp.ICEServers {
		// The TURN server is the one that has credentials.
		if server.Username != "" && server.Credential != "" {
			creds.Username = server.Username
			creds.Password = server.Credential
			return creds, nil
		}
	}

	return nil, fmt.Errorf("could not find a valid TURN server with credentials in Cloudflare API response")
}

// RevokeTURNServerCredentials revokes a specific TURN credential using the Cloudflare API.
func (p *CloudflareProvider) RevokeTURNServerCredentials(ctx context.Context, c *config.TurnProvider, creds *turn.Credentials) error {
	apiKey, ok := c.Options["key_api_token"].(string)
	if !ok || apiKey == "" {
		return fmt.Errorf("cloudflare key_api_token is not configured in options")
	}

	keyID, ok := c.Options["key_id"].(string)
	if !ok || keyID == "" {
		return fmt.Errorf("cloudflare key_id is not configured in options")
	}

	if creds == nil || creds.Username == "" {
		return fmt.Errorf("can't revoke empty username")
	}

	url := fmt.Sprintf(revokeAPIEndpoint, keyID, creds.Username)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create cloudflare revoke http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute cloudflare revoke request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cloudflare revoke API request failed with status %d", resp.StatusCode)
	}

	return nil
}
