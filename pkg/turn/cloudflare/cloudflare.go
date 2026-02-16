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
	cloudflareAPIEndpoint = "https://rtc.live.cloudflare.com/v1/turn/keys/%s/credentials/generate-ice-servers"
	defaultTTL            = 86400 // 24 hours
)

// iceServer defines the structure for a single server in the Cloudflare response.
type iceServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`   // omitempty because STUN servers don't have it
	Credential string   `json:"credential,omitempty"` // omitempty because STUN servers don't have it
	TTL        int      `json:"ttl,omitempty"`
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

func (p *CloudflareProvider) IsEnabled() bool {
	return true
}

// GetTURNServerCredentials fetches temporary TURN credentials from the Cloudflare API.
func (p *CloudflareProvider) GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider) (*turn.Credentials, error) {
	apiKey := c.Credentials.APIKey
	if apiKey == "" {
		return nil, fmt.Errorf("cloudflare api_key (api_token) is not configured")
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
		TTL int `json:"ttl"`
	}{
		TTL: ttl,
	}
	payload, err := json.Marshal(payloadStruct)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cloudflare ttl payload: %w", err)
	}

	url := fmt.Sprintf(cloudflareAPIEndpoint, keyID)
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

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cloudflare API request failed with status %d", resp.StatusCode)
	}

	var cfResp cloudflareResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return nil, fmt.Errorf("failed to decode cloudflare response: %w", err)
	}

	// Find the TURN server entry in the response array.
	for _, server := range cfResp.ICEServers {
		// The TURN server is the one that has credentials.
		if server.Username != "" && server.Credential != "" {
			creds := &turn.Credentials{
				Username: server.Username,
				Password: server.Credential,
				URIs:     server.URLs,
				TTL:      server.TTL,
			}
			return creds, nil
		}
	}

	return nil, fmt.Errorf("could not find a valid TURN server with credentials in Cloudflare API response")
}
