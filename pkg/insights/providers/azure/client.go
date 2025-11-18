package azure

import (
	"context"
	"errors"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// AzureProvider is the main struct that implements the insights.Provider interface.
// It holds clients for the different services Azure offers. For Phase 1, it will only
// hold the client for transcription services.
type AzureProvider struct {
	conf config.ServiceConfig
	log  *logrus.Entry
}

// NewProvider creates a new, fully configured Azure provider.
// It initializes the necessary internal clients based on the provided service configuration.
func NewProvider(conf config.ServiceConfig, log *logrus.Entry) *AzureProvider {
	return &AzureProvider{
		conf: conf,
		log:  log,
	}
}

// CreateTranscription delegates the transcription task to the specialized transcribe client.
// This is a simple pass-through, keeping this file clean and easy to read.
func (p *AzureProvider) CreateTranscription(ctx context.Context, roomId, userId, spokenLang string) (insights.TranscriptionStream, error) {
	// For now, we only initialize the transcription client.
	// The transcribeClient's constructor will extract the credentials it needs from the universal config.
	transcribeClient, err := newTranscribeClient(p.conf.Credentials, p.conf.Model, p.log)
	if err != nil {
		return nil, err
	}

	return transcribeClient.TranscribeStream(ctx, roomId, userId, spokenLang)
}

// Translate is intended for a separate translation provider.
// Since our Phase 1 plan is to use Azure's integrated translation, this method
// will not be used if the config is set correctly (`use_separate_translation_provider: false`).
// We implement it to satisfy the interface, but return an error.
func (p *AzureProvider) Translate(ctx context.Context, text string, targetLangs []string) (map[string]string, error) {
	// This provider relies on integrated translation within the transcription stream.
	// It should not be called directly for translation if configured correctly.
	return nil, errors.New("azure provider is configured for integrated translation; direct translation is not supported")
}
