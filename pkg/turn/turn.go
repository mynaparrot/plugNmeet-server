package turn

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// Credentials holds the response from a TURN provider.
type Credentials struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	URIs     []string `json:"uris"`
	TTL      int      `json:"ttl"`
}

// Provider is the master interface for all TURN service integrations.
type Provider interface {
	IsEnabled() bool
	GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider) (*Credentials, error)
}
