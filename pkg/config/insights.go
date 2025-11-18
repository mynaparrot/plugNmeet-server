package config

// LanguageInfo defines the structure for a single supported language.
type LanguageInfo struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Locale string `json:"locale"`
}

// InsightsConfig is the main config block for the insights feature.
type InsightsConfig struct {
	// The key is the provider type ("azure", "google"), the value is a list of accounts.
	Providers map[string][]ProviderAccount `yaml:"providers"`
	Services  map[string]ServiceConfig     `yaml:"services"`
}

// ProviderAccount defines a single, uniquely identified set of credentials for a provider.
type ProviderAccount struct {
	ID          string            `yaml:"id"`
	Credentials CredentialsConfig `yaml:"credentials"`
}

// ServiceConfig now references a provider type and a specific account ID.
type ServiceConfig struct {
	Provider string `yaml:"provider"`
	ID       string `yaml:"id"`
	Model    string `yaml:"model"`
}

// CredentialsConfig now includes an optional Endpoint.
type CredentialsConfig struct {
	APIKey             string `yaml:"api_key"`
	Region             string `yaml:"region"`
	ServiceAccountFile string `yaml:"service_account_file"`
	Endpoint           string `yaml:"endpoint"`
}
