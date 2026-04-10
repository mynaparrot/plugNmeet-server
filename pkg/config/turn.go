package config

import (
	"fmt"
	"time"
)

// TurnConfig is the main config block for the TURN feature.
type TurnConfig struct {
	// by default false, in that case livekit will handle it
	Enabled               bool                    `yaml:"enabled"`
	Provider              string                  `yaml:"provider"`
	ForceTurn             bool                    `yaml:"force_turn"`
	FallbackTurn          bool                    `yaml:"fallback_turn"`
	FallbackTimerDuration time.Duration           `yaml:"fallback_timer_duration"`
	FallbackOnFlapping    *FallbackOnFlapping     `yaml:"fallback_on_flapping"`
	Providers             map[string]TurnProvider `yaml:"providers"`
}

type FallbackOnFlapping struct {
	Enabled            bool  `yaml:"enabled"`
	MaxPoorConnCount   int32 `yaml:"max_poor_conn_count"`
	CheckDurationInSec int32 `yaml:"check_duration_in_sec"`
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
