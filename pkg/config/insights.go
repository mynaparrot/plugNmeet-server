package config

import (
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// ModelPricing holds pricing information for a service.
type ModelPricing struct {
	InputPricePerMillionTokens  float64 `yaml:"input_price_per_million_tokens"`
	OutputPricePerMillionTokens float64 `yaml:"output_price_per_million_tokens"`
	PricePerMinute              float64 `yaml:"price_per_minute"`
	PricePerMillionCharacters   float64 `yaml:"price_per_million_characters"`
	PricePerHour                float64 `yaml:"price_per_hour"`
}

// InsightsConfig is the main config block for the insights feature.
type InsightsConfig struct {
	// The key is the provider type ("azure", "google"), the value is a list of accounts.
	Providers map[string][]ProviderAccount           `yaml:"providers"`
	Services  map[insights.ServiceType]ServiceConfig `yaml:"services"`
}

// ProviderAccount defines a single, uniquely identified set of credentials for a provider.
type ProviderAccount struct {
	ID          string                 `yaml:"id"`
	Credentials CredentialsConfig      `yaml:"credentials"`
	Options     map[string]interface{} `yaml:"options"` // Generic options for the provider
}

// ServiceConfig now references a provider type and a specific account ID.
type ServiceConfig struct {
	Provider string                  `yaml:"provider"`
	ID       string                  `yaml:"id"`
	Options  map[string]interface{}  `yaml:"options"` // Generic options, e.g., model
	Pricing  map[string]ModelPricing `yaml:"pricing"`
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
	for _, acc := range providerAccounts {
		if acc.ID == serviceConfig.ID {
			found := acc
			return &found, &serviceConfig, nil
		}
	}

	return nil, nil, fmt.Errorf("account with id '%s' not found for provider '%s'", serviceConfig.ID, serviceConfig.Provider)
}

// GetServiceModelPricing is a helper to get pricing for a specific model within a service.
// If a price for the specific modelName is not found, it will fall back to looking for a "default" price.
func (c *InsightsConfig) GetServiceModelPricing(serviceType insights.ServiceType, modelName string) (*ModelPricing, error) {
	// 1. Get the service configuration
	serviceConfig, configOk := c.Services[serviceType]
	if !configOk {
		return nil, fmt.Errorf("service '%s' is not defined in config", serviceType)
	}

	// 2. Check if the pricing block exists.
	if serviceConfig.Pricing == nil {
		return nil, fmt.Errorf("pricing config block not found for service '%s'", serviceType)
	}

	// 3. Find the pricing for the specific model.
	if pricing, ok := serviceConfig.Pricing[modelName]; ok {
		return &pricing, nil
	}

	// 4. If a specific model price is not found, fall back to "default".
	if pricing, ok := serviceConfig.Pricing["default"]; ok {
		return &pricing, nil
	}

	return nil, fmt.Errorf("pricing config not found for model '%s' (or default) in service '%s'", modelName, serviceType)
}
