package turnservice

import (
	"context"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn/cloudflare"
	"github.com/mynaparrot/plugnmeet-server/pkg/turn/coturn"
)

// TurnService is the main entry point for interacting with the TURN framework.
type TurnService struct {
	config   *config.TurnConfig
	provider turn.Provider
}

func New(conf *config.AppConfig) (*TurnService, error) {
	// The service is disabled if the config block is missing or explicitly disabled.
	if conf.TurnServer == nil || !conf.TurnServer.Enabled {
		return &TurnService{
			config:   conf.TurnServer,
			provider: nil, // No provider loaded
		}, nil
	}

	ts := &TurnService{
		config: conf.TurnServer,
		// forceTurn is deprecated, we'll use the value from credentials
	}

	// This factory logic selects the correct provider.
	switch ts.config.Provider {
	case "coturn":
		ts.provider = coturn.NewCoturnProvider()
	case "cloudflare":
		ts.provider = cloudflare.NewCloudflareProvider()
	default:
		return nil, fmt.Errorf("unknown TURN provider: '%s'", ts.config.Provider)
	}

	return ts, nil
}

// GetCredentials returns TURN credentials from the configured provider.
// It returns nil if the service is disabled.
func (s *TurnService) GetCredentials(ctx context.Context, roomId, userId string) (*turn.Credentials, error) {
	if s.config == nil || !s.config.Enabled || s.provider == nil {
		// If the feature is disabled, we simply return nothing.
		// This is not an error condition.
		return nil, nil
	}

	providerConf, err := s.config.GetProvider()
	if err != nil {
		return nil, err
	}

	credentials, err := s.provider.GetTURNServerCredentials(ctx, providerConf, roomId, userId)
	if err != nil {
		return nil, err
	}

	credentials.ForceTurn = s.config.ForceTurn
	// Only enable fallback if force_turn is false
	if !s.config.ForceTurn && s.config.FallbackTurn {
		credentials.FallbackTurn = true
	}

	return credentials, nil
}

// RevokeCredentials revokes TURN credentials using the configured provider.
func (s *TurnService) RevokeCredentials(ctx context.Context, creds *turn.Credentials) error {
	if s.config == nil || !s.config.Enabled || s.provider == nil {
		// If the feature is disabled, we simply return nothing.
		return nil
	}

	providerConf, err := s.config.GetProvider()
	if err != nil {
		return err
	}

	return s.provider.RevokeTURNServerCredentials(ctx, providerConf, creds)
}
