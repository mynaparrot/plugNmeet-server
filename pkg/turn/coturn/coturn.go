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

type CoturnProvider struct{}

func NewCoturnProvider() turn.Provider {
	return &CoturnProvider{}
}

func (p *CoturnProvider) IsEnabled() bool {
	return true
}

// GetTURNServerCredentials for coturn generates credentials based on the shared secret.
func (p *CoturnProvider) GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider) (*turn.Credentials, error) {
	secret, ok := c.Options["shared_secret"].(string)
	if !ok || secret == "" {
		return nil, fmt.Errorf("coturn shared_secret is not configured")
	}

	uris, ok := c.Options["uris"].([]interface{})
	if !ok || len(uris) == 0 {
		return nil, fmt.Errorf("coturn uris are not configured")
	}

	// Standard TURN credential generation
	expiry := time.Now().Add(24 * time.Hour).Unix()
	username := fmt.Sprintf("%d:%s", expiry, "pnm") // User part can be static or unique

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
		TTL:      86400, // 24 hours
	}, nil
}
