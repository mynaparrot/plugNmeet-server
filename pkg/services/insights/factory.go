package insights

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/azure"
	"github.com/sirupsen/logrus"
	// "github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/google"
)

// NewProvider is a factory function that creates and returns the configured AI provider.
// It takes a specific service configuration and initializes the correct implementation.
func NewProvider(providerName string, conf config.ServiceConfig, logger *logrus.Entry) insights.Provider {
	log := logger.WithFields(logrus.Fields{
		"provider": providerName,
	})
	switch providerName {
	case "azure":
		// The Azure provider's constructor will know how to use the universal config.
		return azure.NewProvider(conf, log)
	case "google":
		return nil
	case "openai":
		return nil
	default:
		return nil
	}
}

// NewTask is a factory that returns the correct Task implementation
// based on the service name (e.g., "transcription").
func NewTask(serviceName string, conf config.ServiceConfig, logger *logrus.Entry) (insights.Task, error) {
	switch serviceName {
	case "transcription":
		return NewTranscriptionTask(conf, logger)
	default:
		return nil, fmt.Errorf("unknown insights service task: %s", serviceName)
	}
}
