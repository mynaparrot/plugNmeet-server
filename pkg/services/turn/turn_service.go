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
func (s *TurnService) GetCredentials(ctx context.Context) (*turn.Credentials, error) {
	if s.config == nil || !s.config.Enabled || s.provider == nil {
		// If the feature is disabled, we simply return nothing.
		// This is not an error condition.
		return nil, nil
	}

	providerConf, err := s.config.GetProvider()
	if err != nil {
		return nil, err
	}

	return s.provider.GetTURNServerCredentials(ctx, providerConf)
}
