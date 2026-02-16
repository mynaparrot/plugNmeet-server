package coturn

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn"
)

const (
	defaultTTL = 86400 // 24 hours
)

type CoturnProvider struct{}

func NewCoturnProvider() turn.Provider {
	return &CoturnProvider{}
}

// GetTURNServerCredentials for coturn generates credentials based on the shared secret.
func (p *CoturnProvider) GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider, roomId, userId string) (*turn.Credentials, error) {
	secret, ok := c.Options["shared_secret"].(string)
	if !ok || secret == "" {
		return nil, fmt.Errorf("coturn shared_secret is not configured")
	}

	uris, ok := c.Options["uris"].([]interface{})
	if !ok || len(uris) == 0 {
		return nil, fmt.Errorf("coturn uris are not configured")
	}

	ttl := defaultTTL
	if configTTL, ok := c.Options["ttl"].(int); ok {
		ttl = configTTL
	}

	// Standard TURN credential generation
	expiry := time.Now().Add(time.Duration(ttl) * time.Second).Unix()
	username := fmt.Sprintf("%d:%s:%s", expiry, roomId, userId) // Use roomId and userId for best traceability

	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	password := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	strURIs := make([]string, len(uris))
	for i, v := range uris {
		strURIs[i] = v.(string)
	}

	return &turn.Credentials{
		Username: username,
		Password: password,
		URIs:     strURIs,
		TTL:      ttl,
	}, nil
}

// RevokeTURNServerCredentials for coturn is a no-op because there's no standard API for it.
// The credentials are time-based and will expire automatically.
func (p *CoturnProvider) RevokeTURNServerCredentials(ctx context.Context, c *config.TurnProvider, creds *turn.Credentials) error {
	// Not implemented for coturn, as credentials are not revocable in the same way.
	// They are time-limited and will expire.
	return nil
}
