package insightsservice

import (
	"context"
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
	sub          *nats.Subscription
}

func New(ctx context.Context, conf *config.AppConfig, logger *logrus.Logger, redisService *redisservice.RedisService) *InsightsService {
	return &InsightsService{
		ctx:          ctx,
		conf:         conf,
		logger:       logger.WithField("service", "insights"),
		roomAgents:   make(map[string]*roomAgent),
		redisService: redisService,
	}
}

// SubscribeToTaskRequests is the central handler for all incoming tasks.
func (s *InsightsService) SubscribeToTaskRequests() {
	sub, err := s.conf.NatsConn.Subscribe(InsightsNatsChannel, func(msg *nats.Msg) {
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
		s.logger.WithError(err).Fatalln("failed to subscribe to NATS for insights tasks")
	}
	s.logger.Infof("successfully connected with %s channel", sub.Subject)
	s.sub = sub
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

// ActivateTextTask performs a direct, stateless text-based task using the configured provider.
func (s *InsightsService) ActivateTextTask(ctx context.Context, serviceName string, options []byte) (interface{}, error) {
	// 1. Get the configuration for the requested service.
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider account for service '%s': %w", serviceName, err)
	}

	// 2. Create the appropriate task using the factory.
	task, err := NewTask(serviceName, serviceConfig, targetAccount, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create task for service '%s': %w", serviceName, err)
	}

	// 3. Run the stateless task and return the results channel.
	return task.RunStateless(ctx, options)
}

// ActivateAgentTask publishes a 'start' message to activate a room agent for a long-running task.
func (s *InsightsService) ActivateAgentTask(serviceName, roomName, userId string, options []byte, roomE2EEKey *string) error {
	s.logger.Infof("Publishing start agent task request for service '%s' in room '%s'", serviceName, roomName)
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
		targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(payload.ServiceName)
		if err != nil {
			s.lock.Unlock()
			_ = lock.Unlock(s.ctx)
			return err
		}

		agent, err = newRoomAgent(s.ctx, s.conf, serviceConfig, targetAccount, s.logger, payload.RoomName, payload.ServiceName, payload.RoomE2EEKey)
		if err != nil {
			s.lock.Unlock()
			_ = lock.Unlock(s.ctx)
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
			_ = lock.Unlock(s.ctx)
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

// GetSupportedLanguagesForService returns the list of supported languages for a single, specific service.
func (s *InsightsService) GetSupportedLanguagesForService(serviceName string) ([]insights.LanguageInfo, error) {
	// 1. Get the configuration for the requested service.
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider account for service '%s': %w", serviceName, err)
	}

	// 2. Create a new provider instance on-the-fly.
	provider, err := NewProvider(serviceConfig.Provider, targetAccount, serviceConfig, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider for service '%s': %w", serviceName, err)
	}

	// 3. Call the provider's GetSupportedLanguages method and return the results.
	// Here, we assume the 'serviceName' from the config (e.g., "transcription", "translation")
	// is the canonical name the provider understands.
	return provider.GetSupportedLanguages(serviceName), nil
}

func (s *InsightsService) Shutdown() {
	if s.sub != nil {
		if err := s.sub.Unsubscribe(); err != nil {
			s.logger.WithError(err).Errorln("failed to unsubscribe from NATS")
		}
	}

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
