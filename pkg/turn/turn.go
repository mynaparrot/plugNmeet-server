package turn

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// Credentials holds the response from a TURN provider.
type Credentials struct {
	Username              string   `json:"username"`
	Password              string   `json:"password"`
	URIs                  []string `json:"uris"`
	TTL                   int      `json:"ttl"`
	ForceTurn             bool     `json:"force_turn"`
	FallbackTurn          bool     `json:"fallback_turn"`
	FallbackTimerDuration int64    `json:"fallback_timer_duration"`
}

// Provider is the master interface for all TURN service integrations.
type Provider interface {
	GetTURNServerCredentials(ctx context.Context, c *config.TurnProvider, roomId, userId string) (*Credentials, error)
	RevokeTURNServerCredentials(ctx context.Context, c *config.TurnProvider, creds *Credentials) error
}
