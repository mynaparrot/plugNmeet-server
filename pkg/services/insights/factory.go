package insights

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/azure"
	"github.com/sirupsen/logrus"
)

// NewProvider is a factory function that creates and returns the configured AI provider.
func NewProvider(providerType string, creds *config.CredentialsConfig, model string, logger *logrus.Entry) (insights.Provider, error) {
	log := logger.WithFields(logrus.Fields{
		"provider": providerType,
	})
	switch providerType {
	case "azure":
		return azure.NewProvider(creds, model, log)
	default:
		return nil, fmt.Errorf("unknown AI provider type: %s", providerType)
	}
}

// NewTask is a factory that returns the correct Task implementation.
func NewTask(serviceName string, conf *config.ServiceConfig, creds *config.CredentialsConfig, logger *logrus.Entry) (insights.Task, error) {
	switch serviceName {
	case "transcription":
		return NewTranscriptionTask(conf, creds, logger)
	default:
		return nil, fmt.Errorf("unknown insights service task: %s", serviceName)
	}
}
