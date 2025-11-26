package insightsservice

import (
	"context"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/azure"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/google"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

// NewProvider is a factory function that creates and returns the configured AI provider.
func NewProvider(ctx context.Context, providerType string, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, logger *logrus.Entry) (insights.Provider, error) { // Added ctx
	log := logger.WithFields(logrus.Fields{
		"provider": providerType,
	})
	switch providerType {
	case "azure":
		return azure.NewProvider(providerAccount, serviceConfig, log)
	case "google":
		return google.NewProvider(ctx, providerAccount, serviceConfig, log)
	default:
		return nil, fmt.Errorf("unknown AI provider type: %s", providerType)
	}
}

// NewTask is a factory that returns the correct Task implementation.
func NewTask(serviceType insights.ServiceType, appConf *config.AppConfig, serviceConfig *config.ServiceConfig, providerAccount *config.ProviderAccount, natsService *natsservice.NatsService, redisService *redisservice.RedisService, logger *logrus.Entry) (insights.Task, error) {
	switch serviceType {
	case insights.ServiceTypeTranscription:
		return NewTranscriptionTask(appConf, serviceConfig, providerAccount, natsService, redisService, logger)
	case insights.ServiceTypeTranslation:
		return NewTranslationTask(serviceConfig, providerAccount, logger)
	default:
		return nil, fmt.Errorf("unknown insights service task: %s", serviceType)
	}
}
