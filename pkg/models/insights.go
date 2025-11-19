package models

import (
	"context"
	"fmt"
	"sync"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	InsightsNatsChannel = "plug-n-meet-insights"
	TaskStart           = "start"
	TaskEnd             = "end"
	EndRoomTasks        = "endRoomTasks"
)

type InsightsTaskPayload struct {
	Task        string  `json:"task"` // "start" or "end"
	ServiceName string  `json:"service_name"`
	RoomName    string  `json:"room_name"`
	UserID      string  `json:"user_id"`
	Options     []byte  `json:"options"`
	RoomE2EEKey *string `json:"room_e2ee_key"`
}

type InsightsModel struct {
	ctx          context.Context
	conf         *config.AppConfig
	logger       *logrus.Entry
	lock         sync.RWMutex
	roomAgents   map[string]*insightsservice.RoomAgent // Maps a unique key (roomName_serviceName) to a dedicated agent
	redisService *redisservice.RedisService
}

func NewInsightsModel(ctx context.Context, conf *config.AppConfig, redisService *redisservice.RedisService, logger *logrus.Logger) *InsightsModel {
	return &InsightsModel{
		ctx:          ctx,
		conf:         conf,
		logger:       logger.WithField("model", "insights"),
		roomAgents:   make(map[string]*insightsservice.RoomAgent),
		redisService: redisService,
	}
}

// ActivateTextTask performs a direct, stateless text-based task using the configured provider.
func (s *InsightsModel) ActivateTextTask(ctx context.Context, serviceName string, options []byte) error {
	// 1. Get the configuration for the requested service.
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(serviceName)
	if err != nil {
		return fmt.Errorf("failed to get provider account for service '%s': %w", serviceName, err)
	}

	// 2. Create the appropriate task using the factory.
	task, err := insightsservice.NewTask(serviceName, serviceConfig, targetAccount, s.logger)
	if err != nil {
		return fmt.Errorf("failed to create task for service '%s': %w", serviceName, err)
	}

	// 3. Run the stateless task and return the results channel.
	return task.RunStateless(ctx, options)
}

// GetSupportedLanguagesForService returns the list of supported languages for a single, specific service.
func (s *InsightsModel) GetSupportedLanguagesForService(serviceName string) ([]insights.LanguageInfo, error) {
	// 1. Get the configuration for the requested service.
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider account for service '%s': %w", serviceName, err)
	}

	// 2. Create a new provider instance on-the-fly.
	provider, err := insightsservice.NewProvider(serviceConfig.Provider, targetAccount, serviceConfig, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider for service '%s': %w", serviceName, err)
	}

	// 3. Call the provider's GetSupportedLanguages method and return the results.
	// Here, we assume the 'serviceName' from the config (e.g., "transcription", "translation")
	// is the canonical name the provider understands.
	return provider.GetSupportedLanguages(serviceName), nil
}

func (s *InsightsModel) Shutdown() {
	s.lock.RLock()
	// Find all agents that belong to this room without holding a write lock for the whole loop.
	toShutdown := make([]string, 0)
	for key := range s.roomAgents {
		toShutdown = append(toShutdown, key)
	}
	s.lock.RUnlock()

	// Now, call the safe shutdown method for each.
	for _, key := range toShutdown {
		s.shutdownAndRemoveAgent(key)
	}

	s.logger.Infoln("Insights Service shutdown complete.")
}
