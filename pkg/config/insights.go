package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

type ProviderType string

const (
	ProviderAzure  ProviderType = "azure"
	ProviderGoogle ProviderType = "google"
	ProviderOpenAI ProviderType = "openai"

	// avatar providers, consumed by the realtime avatar of the AI agent
	ProviderAnam           ProviderType = "anam"
	ProviderBeyondPresence ProviderType = "beyond_presence"
	ProviderSimli          ProviderType = "simli"
	ProviderTavus          ProviderType = "tavus"
)

// ModelPricing holds pricing information for a service. The token rates are a
// matrix: the input/output rates are the text rates, and every modality with a
// rate of its own overrides them. A modality without a rate is billed as text,
// and a cached rate applies only when it is configured, so a missing value
// never implies a discount.
type ModelPricing struct {
	InputPricePerMillionTokens       float64 `yaml:"input_price_per_million_tokens"`
	OutputPricePerMillionTokens      float64 `yaml:"output_price_per_million_tokens"`
	CachedInputPricePerMillionTokens float64 `yaml:"cached_input_price_per_million_tokens"`
	// Audio and image input tokens. image_input_price_per_million_tokens also
	// covers video input, which every provider prices like an image.
	AudioInputPricePerMillionTokens       float64 `yaml:"audio_input_price_per_million_tokens"`
	AudioCachedInputPricePerMillionTokens float64 `yaml:"audio_cached_input_price_per_million_tokens"`
	ImageInputPricePerMillionTokens       float64 `yaml:"image_input_price_per_million_tokens"`
	ImageCachedInputPricePerMillionTokens float64 `yaml:"image_cached_input_price_per_million_tokens"`
	AudioOutputPricePerMillionTokens      float64 `yaml:"audio_output_price_per_million_tokens"`
	// Cache writes are the input tokens a provider stored in its prompt cache;
	// without a rate of their own they are billed as uncached input.
	CacheWriteInputPricePerMillionTokens      float64 `yaml:"cache_write_input_price_per_million_tokens"`
	AudioCacheWriteInputPricePerMillionTokens float64 `yaml:"audio_cache_write_input_price_per_million_tokens"`
	ImageCacheWriteInputPricePerMillionTokens float64 `yaml:"image_cache_write_input_price_per_million_tokens"`
	PricePerMinute                            float64 `yaml:"price_per_minute"`
	PricePerMillionCharacters                 float64 `yaml:"price_per_million_characters"`
	PricePerHour                              float64 `yaml:"price_per_hour"`
}

// InsightsConfig is the main config block for the insights feature.
type InsightsConfig struct {
	Enabled bool `yaml:"enabled"`
	// The key is the provider type ("azure", "google", "openai", "beyond_presence",
	// "anam", "simli", "tavus"), the value is a list of accounts.
	Providers map[ProviderType][]ProviderAccount      `yaml:"providers"`
	Services  map[insights.ServiceType]*ServiceConfig `yaml:"services"`
}

// ProviderAccount defines a single, uniquely identified set of credentials for a provider.
type ProviderAccount struct {
	ID          string                 `yaml:"id"`
	Credentials CredentialsConfig      `yaml:"credentials"`
	Options     map[string]interface{} `yaml:"options"` // Generic options for the provider
}

// GetOptionsString is a helper to safely get a string value from the generic options map.
func (pa *ProviderAccount) GetOptionsString(key, defaultValue string) string {
	if pa.Options != nil {
		if val, ok := pa.Options[key].(string); ok {
			return val
		}
	}
	return defaultValue
}

// ServiceConfig now references a provider type and a specific account ID.
type ServiceConfig struct {
	Provider ProviderType            `yaml:"provider"`
	ID       string                  `yaml:"id"`
	Options  map[string]interface{}  `yaml:"options"` // Generic options, e.g., model
	Pricing  map[string]ModelPricing `yaml:"pricing"`
}

// GetOptionsString is a helper to safely get a string value from the generic options map.
func (sc *ServiceConfig) GetOptionsString(key, defaultValue string) string {
	if sc.Options != nil {
		if val, ok := sc.Options[key].(string); ok {
			return val
		}
	}
	return defaultValue
}

func (sc *ServiceConfig) GetIntOption(key string, fallback int) int {
	if sc.Options == nil {
		return fallback
	}

	raw := strings.TrimSpace(sc.GetOptionsString(key, fmt.Sprintf("%d", fallback)))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}

	return value
}

// GetVoiceMappings safely extracts the voice mappings from the generic options map.
func (sc *ServiceConfig) GetVoiceMappings() map[string]string {
	mappings := make(map[string]string)
	if sc.Options == nil {
		return mappings
	}

	for key, val := range sc.Options {
		if v, ok := val.(string); ok {
			if strings.HasPrefix(key, "voice-") {
				lang := strings.TrimPrefix(key, "voice-")
				mappings[lang] = v
			}
		}
	}
	return mappings
}

// CredentialsConfig now only contains the most common credential fields.
// can use the Options field if needed extra data
type CredentialsConfig struct {
	APIKey string `yaml:"api_key"`
	Region string `yaml:"region"`
}

// GetProviderAccountForService is a helper to find the correct provider account configuration for a given service.
func (c *InsightsConfig) GetProviderAccountForService(serviceType insights.ServiceType) (*ProviderAccount, *ServiceConfig, error) {
	// 1. Get the service configuration
	serviceConfig, configOk := c.Services[serviceType]
	if !configOk {
		return nil, nil, fmt.Errorf("service '%s' is not defined in config", serviceType)
	}

	// 2. Get the list of accounts for the provider type
	providerAccounts, providerOk := c.Providers[serviceConfig.Provider]
	if !providerOk {
		return nil, nil, fmt.Errorf("provider '%s' (referenced by service '%s') is not defined in config", serviceConfig.Provider, serviceType)
	}

	// 3. Find the specific account within the list by its ID.
	for i := range providerAccounts {
		if providerAccounts[i].ID == serviceConfig.ID {
			return &providerAccounts[i], serviceConfig, nil
		}
	}

	return nil, nil, fmt.Errorf("account with id '%s' not found for provider '%s'", serviceConfig.ID, serviceConfig.Provider)
}

// GetProviderAccount returns the first configured account for providerType
// (or the account matching accountID when non-empty).
func (c *InsightsConfig) GetProviderAccount(providerType ProviderType, accountID string) (*ProviderAccount, error) {
	if c == nil {
		return nil, fmt.Errorf("insights config is not enabled")
	}
	accounts, ok := c.Providers[providerType]
	if !ok || len(accounts) == 0 {
		return nil, fmt.Errorf("provider '%s' is not defined in config", providerType)
	}
	if accountID == "" {
		return &accounts[0], nil
	}
	for i := range accounts {
		if accounts[i].ID == accountID {
			return &accounts[i], nil
		}
	}

	return nil, fmt.Errorf("account with id '%s' not found for provider '%s'", accountID, providerType)
}

// GetServiceModelPricing returns the pricing of the first pricing key that is
// configured for the service, in the given order. A service can price usage by
// role (e.g. "realtime_model") or by concrete model name. If none of the keys
// matches, it falls back to the "default" price.
func (c *InsightsConfig) GetServiceModelPricing(serviceType insights.ServiceType, pricingKeys ...string) (*ModelPricing, error) {
	// 1. Get the service configuration
	serviceConfig, configOk := c.Services[serviceType]
	if !configOk {
		return nil, fmt.Errorf("service '%s' is not defined in config", serviceType)
	}

	// 2. Check if the pricing block exists.
	if serviceConfig.Pricing == nil {
		return nil, fmt.Errorf("pricing config block not found for service '%s'", serviceType)
	}

	// 3. Find the pricing for the first configured key.
	for _, key := range pricingKeys {
		if key == "" {
			continue
		}
		if pricing, ok := serviceConfig.Pricing[key]; ok {
			// Make a safe copy to return to avoid returning a pointer to local range/map temporary copy
			return new(pricing), nil
		}
	}

	// 4. If no key is priced, fall back to "default".
	if pricing, ok := serviceConfig.Pricing["default"]; ok {
		return new(pricing), nil
	}

	return nil, fmt.Errorf("pricing config not found for %v (or default) in service '%s'", pricingKeys, serviceType)
}
