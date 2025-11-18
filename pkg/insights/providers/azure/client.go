package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// TranscriptionOptions defines the structure for options passed to the transcription service.
type TranscriptionOptions struct {
	Language string `json:"language"`
}

// AzureProvider is the main struct that implements the insights.Provider interface.
type AzureProvider struct {
	creds  config.CredentialsConfig // Store the specific credentials for this provider instance
	model  string                   // Store the model from the service config
	logger *logrus.Entry
}

// NewProvider now accepts the specific credentials for this provider instance.
func NewProvider(creds config.CredentialsConfig, model string, log *logrus.Entry) (*AzureProvider, error) {
	return &AzureProvider{
		creds:  creds,
		model:  model,
		logger: log,
	}, nil
}

// CreateTranscription now uses the stored credentials and parses the options.
func (p *AzureProvider) CreateTranscription(ctx context.Context, roomID, userID string, options []byte) (insights.TranscriptionStream, error) {
	opts := &TranscriptionOptions{
		Language: "en-US", // Default language
	}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	// Use the stored credentials and model to create the client.
	transcribeClient, err := newTranscribeClient(p.creds, p.model, p.logger)
	if err != nil {
		return nil, err
	}

	return transcribeClient.CreateTranscription(ctx, roomID, userID, opts.Language)
}

// Translate remains the same.
func (p *AzureProvider) Translate(ctx context.Context, text string, targetLangs []string) (map[string]string, error) {
	return nil, errors.New("azure provider is configured for integrated translation; direct translation is not supported")
}

// GetSupportedLanguages implements the insights.Provider interface.
// It looks up the service name in the hard-coded map from languages.go.
func (p *AzureProvider) GetSupportedLanguages(serviceName string) []config.LanguageInfo {
	if langs, ok := supportedLanguages[serviceName]; ok {
		return langs
	}
	// Return an empty slice if the service is not found for this provider.
	return []config.LanguageInfo{}
}
