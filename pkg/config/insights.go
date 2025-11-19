package config

// InsightsConfig is the main config block for the insights feature.
type InsightsConfig struct {
	// The key is the provider type ("azure", "google"), the value is a list of accounts.
	Providers map[string][]ProviderAccount `yaml:"providers"`
	Services  map[string]ServiceConfig     `yaml:"services"`
}

// ProviderAccount defines a single, uniquely identified set of credentials for a provider.
type ProviderAccount struct {
	ID          string                 `yaml:"id"`
	Credentials CredentialsConfig      `yaml:"credentials"`
	Options     map[string]interface{} `yaml:"options"` // Generic options for the provider
}

// ServiceConfig now references a provider type and a specific account ID.
type ServiceConfig struct {
	Provider string                 `yaml:"provider"`
	ID       string                 `yaml:"id"`
	Options  map[string]interface{} `yaml:"options"` // Generic options, e.g., model
}

// CredentialsConfig now only contains the most common credential fields.
// can use the Options field if needed extra data
type CredentialsConfig struct {
	APIKey string `yaml:"api_key"`
	Region string `yaml:"region"`
}
