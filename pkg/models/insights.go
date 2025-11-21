package models

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	InsightsNatsChannel           = "plug-n-meet-insights"
	TaskConfigureAgent            = "configureAgent"
	TaskStart                     = "start"
	TaskEnd                       = "end"
	TaskEndRoomAgentByServiceName = "endRoomAgentByServiceName"
	TaskEndRoomAllAgents          = "endRoomAllAgents"
)

type AgentTaskResponse struct {
	Status bool   `json:"status"`
	Msg    string `json:"msg"`
}

type InsightsTaskPayload struct {
	Task        string          `json:"task"`
	ServiceName string          `json:"service_name"`
	RoomName    string          `json:"room_name"`
	UserID      string          `json:"user_id"`
	Options     []byte          `json:"options"`
	RoomE2EEKey *string         `json:"room_e2ee_key"`
	TargetUsers map[string]bool `json:"target_users,omitempty"` // NEW
}

type InsightsModel struct {
	ctx          context.Context
	conf         *config.AppConfig
	logger       *logrus.Entry
	lock         sync.RWMutex
	roomAgents   map[string]*insightsservice.RoomAgent // Maps a unique key (roomName@serviceName) to a dedicated agent
	redisService *redisservice.RedisService
	natsService  *natsservice.NatsService
}

func NewInsightsModel(ctx context.Context, conf *config.AppConfig, redisService *redisservice.RedisService, natsService *natsservice.NatsService, logger *logrus.Logger) *InsightsModel {
	return &InsightsModel{
		ctx:          ctx,
		conf:         conf,
		redisService: redisService,
		natsService:  natsService,
		roomAgents:   make(map[string]*insightsservice.RoomAgent),
		logger:       logger.WithField("model", "insights"),
	}
}

// ConfigureAgent sends a configuration task and waits for confirmation.
func (s *InsightsModel) ConfigureAgent(serviceName, roomName string, allowedUsers []string, timeout time.Duration) error {
	s.logger.Infof("Sending request to configure agent for service '%s' in room '%s'", serviceName, roomName)

	usersMap := make(map[string]bool)
	for _, user := range allowedUsers {
		usersMap[user] = true
	}

	payload := &InsightsTaskPayload{
		Task:        TaskConfigureAgent,
		ServiceName: serviceName,
		RoomName:    roomName,
		TargetUsers: usersMap,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// Use nats request/reply
	msg, err := s.conf.NatsConn.Request(InsightsNatsChannel, p, timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}

	var res AgentTaskResponse
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return fmt.Errorf("failed to parse response from agent: %w", err)
	}

	if !res.Status {
		return fmt.Errorf("agent failed to process task: %s", res.Msg)
	}

	return nil // Success!
}

// ActivateAgentTaskForUser publishes a 'start' message to activate a room agent for a long-running task for a specific user.
func (s *InsightsModel) ActivateAgentTaskForUser(serviceName, roomName, userId string, options []byte, roomE2EEKey *string, timeout time.Duration) error {
	s.logger.Infof("Publishing start agent task request for service '%s' in room '%s' for user '%s'", serviceName, roomName, userId)
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

	msg, err := s.conf.NatsConn.Request(InsightsNatsChannel, p, timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}

	var res AgentTaskResponse
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return fmt.Errorf("failed to parse response from agent: %w", err)
	}

	if !res.Status {
		return fmt.Errorf("agent failed to process task: %s", res.Msg)
	}

	return nil
}

// EndAgentTaskForUser now only publishes an 'end' message.
func (s *InsightsModel) EndAgentTaskForUser(serviceName, roomName, userId string, timeout time.Duration) error {
	s.logger.Infof("Publishing end task request for service '%s' in room '%s' for user '%s'", serviceName, roomName, userId)
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

	msg, err := s.conf.NatsConn.Request(InsightsNatsChannel, p, timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}

	var res AgentTaskResponse
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return fmt.Errorf("failed to parse response from agent: %w", err)
	}

	if !res.Status {
		return fmt.Errorf("agent failed to process task: %s", res.Msg)
	}

	return nil // Success!
}

func (s *InsightsModel) EndRoomAgentTaskByServiceNameAndWait(serviceName, roomName string, timeout time.Duration) error {
	s.logger.Infof("Publishing end task request for service '%s' in room '%s'", serviceName, roomName)
	payload := &InsightsTaskPayload{
		Task:        TaskEndRoomAgentByServiceName,
		ServiceName: serviceName,
		RoomName:    roomName,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg, err := s.conf.NatsConn.Request(InsightsNatsChannel, p, timeout)
	if err != nil {
		return fmt.Errorf("NATS request failed: %w", err)
	}

	var res AgentTaskResponse
	if err := json.Unmarshal(msg.Data, &res); err != nil {
		return fmt.Errorf("failed to parse response from agent: %w", err)
	}

	if !res.Status {
		return fmt.Errorf("agent failed to process task: %s", res.Msg)
	}

	return nil // Success!
}

// EndRoomAllAgentTasks will close everything for this room
func (s *InsightsModel) EndRoomAllAgentTasks(roomName string) error {
	s.logger.Infof("Publishing end all room tasks request for room '%s'", roomName)
	payload := &InsightsTaskPayload{
		Task: TaskEndRoomAllAgents,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.conf.NatsConn.Publish(InsightsNatsChannel, p)
}

// ActivateTextTask performs a direct, stateless text-based task using the configured provider.
func (s *InsightsModel) ActivateTextTask(ctx context.Context, serviceName string, options []byte) (interface{}, error) {
	// 1. Get the configuration for the requested service.
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(serviceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider account for service '%s': %w", serviceName, err)
	}

	// 2. Create the appropriate task using the factory.
	task, err := insightsservice.NewTask(serviceName, serviceConfig, targetAccount, s.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create task for service '%s': %w", serviceName, err)
	}

	// 3. Run the stateless task and return the results channel.
	return task.RunStateless(ctx, options)
}

// GetSupportedLanguagesForService returns the list of supported languages for a single, specific service.
func (s *InsightsModel) GetSupportedLanguagesForService(serviceName string) ([]*plugnmeet.InsightsSupportedLangInfo, error) {
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
