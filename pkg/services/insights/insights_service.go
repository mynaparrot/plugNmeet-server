package insights

import (
	"fmt"
	"strings"
	"sync"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

// InsightsService is a long-lived manager that dispatches requests to single-purpose, room-specific agents.
type InsightsService struct {
	ctx        context.Context
	conf       *config.AppConfig
	logger     *logrus.Entry
	lock       sync.RWMutex
	roomAgents map[string]*roomAgent // Maps a unique key (roomName_serviceName) to a dedicated agent
}

func NewInsightsService(ctx context.Context, conf *config.AppConfig, logger *logrus.Logger) *InsightsService {
	return &InsightsService{
		ctx:        ctx,
		conf:       conf,
		logger:     logger.WithField("service", "insights"),
		roomAgents: make(map[string]*roomAgent),
	}
}

// getAgentKey creates a unique identifier for an agent.
func getAgentKey(roomName, serviceName string) string {
	return fmt.Sprintf("%s_%s", roomName, serviceName)
}

// ActivateTask is the main entry point. It finds or creates the specific agent for the requested service and room.
func (s *InsightsService) ActivateTask(serviceName, roomName, userId string) error {
	key := getAgentKey(roomName, serviceName)

	s.lock.Lock()
	agent, ok := s.roomAgents[key]
	if !ok {
		// Agent does not exist for this service in this room. Create it now.
		s.logger.Infof("no agent found for service '%s' in room %s, creating a new one", serviceName, roomName)

		serviceConfig, configOk := s.conf.Insights.Services[serviceName]
		if !configOk {
			s.lock.Unlock()
			return fmt.Errorf("service '%s' is not defined in config", serviceName)
		}

		var err error
		agent, err = newRoomAgent(s.ctx, s.conf, s.logger, roomName, serviceName, serviceConfig)
		if err != nil {
			s.lock.Unlock()
			return fmt.Errorf("failed to create insights agent for service '%s' in room %s: %w", serviceName, roomName, err)
		}
		s.roomAgents[key] = agent
	}
	s.lock.Unlock()

	// Now, delegate the task activation to the single-purpose agent.
	return agent.ActivateTaskForUser(userId)
}

// EndTask finds the correct agent and tells it to end tasks for a user.
func (s *InsightsService) EndTask(serviceName, roomName, userId string) {
	key := getAgentKey(roomName, serviceName)
	s.lock.RLock()
	agent, ok := s.roomAgents[key]
	s.lock.RUnlock()

	if ok {
		agent.EndTasksForUser(userId)
	}
}

// RemoveAgentForRoom is called when a room ends. It finds and shuts down ALL agents associated with that room.
func (s *InsightsService) RemoveAgentForRoom(roomName string) {
	s.lock.Lock()
	// Find all agents that belong to this room.
	keysToDelete := make([]string, 0)
	for key, agent := range s.roomAgents {
		if strings.HasPrefix(key, roomName+"_") {
			agent.Shutdown()
			keysToDelete = append(keysToDelete, key)
		}
	}

	// Delete them from the map.
	for _, key := range keysToDelete {
		delete(s.roomAgents, key)
	}
	s.lock.Unlock()

	if len(keysToDelete) > 0 {
		s.logger.Infof("removed %d insights agents for room %s", len(keysToDelete), roomName)
	}
}
