package config

import "fmt"

// TurnConfig is the main config block for the TURN feature.
type TurnConfig struct {
	// by default false, in that case livekit will handle it
	Enabled      bool                    `yaml:"enabled"`
	Provider     string                  `yaml:"provider"`
	ForceTurn    bool                    `yaml:"force_turn"`
	FallbackTurn bool                    `yaml:"fallback_turn"`
	Providers    map[string]TurnProvider `yaml:"providers"`
}

// TurnProvider holds the configuration for a single provider.
type TurnProvider struct {
	Options map[string]interface{} `yaml:"options"`
}

// GetProvider returns the configuration for the currently active provider.
func (t *TurnConfig) GetProvider() (*TurnProvider, error) {
	if !t.Enabled {
		return nil, fmt.Errorf("turn is not enabled")
	}
	providerConfig, ok := t.Providers[t.Provider]
	if !ok {
		return nil, fmt.Errorf("turn provider '%s' is not defined in config", t.Provider)
	}
	return &providerConfig, nil
}
