package insightsservice

import (
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/azure"
	"github.com/sirupsen/logrus"
)

// NewProvider is a factory function that creates and returns the configured AI provider.
func NewProvider(providerType string, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, logger *logrus.Entry) (insights.Provider, error) {
	log := logger.WithFields(logrus.Fields{
		"provider": providerType,
	})
	switch providerType {
	case "azure":
		return azure.NewProvider(providerAccount, serviceConfig, log)
	default:
		return nil, fmt.Errorf("unknown AI provider type: %s", providerType)
	}
}

// NewTask is a factory that returns the correct Task implementation.
func NewTask(serviceType insights.ServiceType, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, logger *logrus.Entry) (insights.Task, error) {
	switch serviceType {
	case insights.ServiceTypeTranscription:
		return NewTranscriptionTask(serviceConfig, providerAccount, logger)
	case insights.ServiceTypeTranslation:
		return NewTranslationTask(serviceConfig, providerAccount, logger)
	default:
		return nil, fmt.Errorf("unknown insights service task: %s", serviceType)
	}
}
