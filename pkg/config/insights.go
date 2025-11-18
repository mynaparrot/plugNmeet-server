package config

// This file defines the configuration structures for the Insights service.
// These structs are used by both the main service and the provider implementations
// to avoid circular dependencies.

// InsightsConfig is the top-level configuration for the entire service.
type InsightsConfig struct {
	// Services is a map where the key is the name of the service (e.g., "transcription").
	Services map[string]ServiceConfig `yaml:"services"`
}

// ServiceConfig defines a single AI service, its provider, model, and credentials.
type ServiceConfig struct {
	Provider    string            `yaml:"provider"`
	Model       string            `yaml:"model"`
	Credentials CredentialsConfig `yaml:"credentials"`
}

// CredentialsConfig is the UNIVERSAL credentials block.
type CredentialsConfig struct {
	APIKey             string `yaml:"api_key"`
	APIEndpoint        string `yaml:"api_endpoint"`
	Region             string `yaml:"region"`
	ProjectID          string `yaml:"project_id"`
	ServiceAccountJSON string `yaml:"service_account_json"`
}
