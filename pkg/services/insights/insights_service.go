package insights

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

const (
	InsightsNatsChannel = "plug-n-meet-insights"
	TaskStart           = "start"
	TaskEnd             = "end"
)

type InsightsTaskPayload struct {
	Task        string  `json:"task"` // "start" or "end"
	ServiceName string  `json:"service_name"`
	RoomName    string  `json:"room_name"`
	UserID      string  `json:"user_id"`
	Options     []byte  `json:"options"`
	RoomE2EEKey *string `json:"room_e2ee_key"`
}

type InsightsService struct {
	ctx          context.Context
	conf         *config.AppConfig
	logger       *logrus.Entry
	lock         sync.RWMutex
	roomAgents   map[string]*roomAgent // Maps a unique key (roomName_serviceName) to a dedicated agent
	redisService *redisservice.RedisService
}

func NewInsightsService(ctx context.Context, conf *config.AppConfig, logger *logrus.Logger, redisService *redisservice.RedisService) *InsightsService {
	s := &InsightsService{
		ctx:          ctx,
		conf:         conf,
		logger:       logger.WithField("service", "insights"),
		roomAgents:   make(map[string]*roomAgent),
		redisService: redisService,
	}

	// Create the single subscription for this server instance.
	go s.subscribeToTaskRequests()

	return s
}

// subscribeToTaskRequests is the central handler for all incoming tasks.
func (s *InsightsService) subscribeToTaskRequests() {
	_, err := s.conf.NatsConn.Subscribe(InsightsNatsChannel, func(msg *nats.Msg) {
		var payload InsightsTaskPayload
		err := json.Unmarshal(msg.Data, &payload)
		if err != nil {
			s.logger.WithError(err).Error("failed to unmarshal insights task payload")
			return
		}

		s.logger.Infof("received task '%s' for service '%s' in room '%s'", payload.Task, payload.ServiceName, payload.RoomName)
		s.handleIncomingTask(&payload)
	})
	if err != nil {
		s.logger.WithError(err).Error("failed to subscribe to NATS for insights tasks")
	}
}

// handleIncomingTask is the core logic that runs on every server.
func (s *InsightsService) handleIncomingTask(payload *InsightsTaskPayload) {
	if payload.Task == TaskEnd {
		s.endLocalAgentTask(payload.ServiceName, payload.RoomName, payload.UserID)
		return
	}

	if payload.Task == TaskStart {
		lockKey := getAgentKey(payload.RoomName, payload.ServiceName)
		lock := s.redisService.NewLock(lockKey, 30*time.Second)

		isLeader, err := lock.TryLock(s.ctx)
		if err != nil {
			s.logger.WithError(err).Error("failed leader election attempt")
			return
		}

		if isLeader {
			s.logger.Infof("Acquired leadership for task '%s'", lockKey)
			if err := s.manageLocalAgent(payload, lock); err != nil {
				s.logger.WithError(err).Error("failed to manage local agent")
			}
		}
	}
}

// endLocalAgentTask is the internal method for the leader to use.
func (s *InsightsService) endLocalAgentTask(serviceName, roomName, userId string) {
	key := getAgentKey(roomName, serviceName)
	s.lock.RLock()
	agent, ok := s.roomAgents[key]
	s.lock.RUnlock()

	if ok {
		agent.EndTasksForUser(userId)
	}
}

// getAgentKey creates a unique identifier for an agent.
func getAgentKey(roomName, serviceName string) string {
	return fmt.Sprintf("insights:%s_%s", roomName, serviceName)
}

// getProviderAccountForService is a helper to find the correct provider account configuration for a given service.
func (s *InsightsService) getProviderAccountForService(serviceName string) (*config.ProviderAccount, *config.ServiceConfig, error) {
	// 1. Get the service configuration
	serviceConfig, configOk := s.conf.Insights.Services[serviceName]
	if !configOk {
		return nil, nil, fmt.Errorf("service '%s' is not defined in config", serviceName)
	}

	// 2. Get the list of accounts for the provider type
	providerAccounts, providerOk := s.conf.Insights.Providers[serviceConfig.Provider]
	if !providerOk {
		return nil, nil, fmt.Errorf("provider '%s' (referenced by service '%s') is not defined in config", serviceConfig.Provider, serviceName)
	}

	// 3. Find the specific account within the list by its ID.
	for _, acc := range providerAccounts {
		if acc.ID == serviceConfig.ID {
			found := acc
			return &found, &serviceConfig, nil
		}
	}

	return nil, nil, fmt.Errorf("account with id '%s' not found for provider '%s'", serviceConfig.ID, serviceConfig.Provider)
}

// ActivateTask now only publishes a 'start' message.
func (s *InsightsService) ActivateTask(serviceName, roomName, userId string, options []byte, roomE2EEKey *string) error {
	s.logger.Infof("Publishing start task request for service '%s' in room '%s'", serviceName, roomName)
	payload := &InsightsTaskPayload{
		Task:        TaskStart,
		ServiceName: serviceName,
		RoomName:    roomName,
		UserID:      userId,
		Options:     options,
		RoomE2EEKey: roomE2EEKey,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.conf.NatsConn.Publish(InsightsNatsChannel, p)
}

// EndTask now only publishes an 'end' message.
func (s *InsightsService) EndTask(serviceName, roomName, userId string) error {
	s.logger.Infof("Publishing end task request for service '%s' in room '%s'", serviceName, roomName)
	payload := &InsightsTaskPayload{
		Task:        TaskEnd,
		ServiceName: serviceName,
		RoomName:    roomName,
		UserID:      userId,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.conf.NatsConn.Publish(InsightsNatsChannel, p)
}

// manageLocalAgent now uses the helper method.
func (s *InsightsService) manageLocalAgent(payload *InsightsTaskPayload, lock *redisservice.Lock) error {
	key := getAgentKey(payload.RoomName, payload.ServiceName)

	s.lock.Lock()
	agent, ok := s.roomAgents[key]
	if !ok {
		s.logger.Infof("no agent found for service '%s' in room %s, creating a new one", payload.ServiceName, payload.RoomName)

		// Use the new helper method to get both configs
		targetAccount, serviceConfig, err := s.getProviderAccountForService(payload.ServiceName)
		if err != nil {
			s.lock.Unlock()
			lock.Unlock(s.ctx)
			return err
		}

		agent, err = newRoomAgent(s.ctx, s.conf, s.logger, payload.RoomName, payload.ServiceName, serviceConfig, &targetAccount.Credentials, payload.RoomE2EEKey)
		if err != nil {
			s.lock.Unlock()
			lock.Unlock(s.ctx)
			return fmt.Errorf("failed to create insights agent: %w", err)
		}
		s.roomAgents[key] = agent

		go s.superviseAgent(agent, lock)
	}
	s.lock.Unlock()

	return agent.ActivateTaskForUser(payload.UserID, payload.Options)
}

// superviseAgent is the "Janitor" that maintains leadership.
func (s *InsightsService) superviseAgent(agent *roomAgent, lock *redisservice.Lock) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	s.logger.Infof("Supervisor started for agent '%s' in room '%s'", agent.serviceName, agent.room.Name())
	key := getAgentKey(agent.room.Name(), agent.serviceName)

	for {
		select {
		case <-ticker.C:
			if err := lock.Refresh(s.ctx); err != nil {
				s.logger.Warnf("Lost leadership for agent '%s', shutting down.", agent.serviceName)
				s.shutdownAndRemoveAgent(key)
				return
			}
		case <-agent.ctx.Done():
			s.logger.Infof("Agent for '%s' has shut down, releasing leadership.", agent.serviceName)
			lock.Unlock(s.ctx)
			s.shutdownAndRemoveAgent(key)
			return
		}
	}
}

// shutdownAndRemoveAgent is the internal method that safely shuts down and removes a single agent.
func (s *InsightsService) shutdownAndRemoveAgent(key string) {
	s.lock.Lock()
	agent, ok := s.roomAgents[key]
	if ok {
		delete(s.roomAgents, key)
	}
	s.lock.Unlock()

	if ok {
		agent.Shutdown()
		s.logger.Infof("removed and shut down agent for key %s", key)
	}
}

// RemoveAgentForRoom now uses the new shutdownAndRemoveAgent method.
func (s *InsightsService) RemoveAgentForRoom(roomName string) {
	s.lock.RLock()
	// Find all agents that belong to this room without holding a write lock for the whole loop.
	keysToDelete := make([]string, 0)
	for key := range s.roomAgents {
		if strings.HasPrefix(key, fmt.Sprintf("insights:%s_", roomName)) {
			keysToDelete = append(keysToDelete, key)
		}
	}
	s.lock.RUnlock()

	// Now, call the safe shutdown method for each key.
	for _, key := range keysToDelete {
		s.shutdownAndRemoveAgent(key)
	}

	if len(keysToDelete) > 0 {
		s.logger.Infof("removed %d insights agents for room %s", len(keysToDelete), roomName)
	}
}

// GetSupportedLanguagesForServices returns a map where the key is the service name
// and the value is the list of supported languages for the provider configured for that service.
func (s *InsightsService) GetSupportedLanguagesForServices() map[string][]config.LanguageInfo {
	response := make(map[string][]config.LanguageInfo)
	providerInstances := make(map[string]insights.Provider)

	for serviceName := range s.conf.Insights.Services {
		// Use the new helper method
		targetAccount, serviceConfig, err := s.getProviderAccountForService(serviceName)
		if err != nil {
			s.logger.WithError(err).Warnf("could not get provider account for service %s", serviceName)
			continue
		}

		providerKey := fmt.Sprintf("%s_%s", serviceConfig.Provider, serviceConfig.ID)
		provider, ok := providerInstances[providerKey]
		if !ok {
			provider, err = NewProvider(serviceConfig.Provider, &targetAccount.Credentials, serviceConfig.Model, s.logger)
			if err != nil {
				s.logger.WithError(err).Warnf("could not create provider for service %s", serviceName)
				continue
			}
			providerInstances[providerKey] = provider
		}

		response[serviceName] = provider.GetSupportedLanguages(serviceName)
	}

	return response
}
