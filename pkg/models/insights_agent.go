package models

import (
	"fmt"
	"strings"
	"time"

	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// getAgentKey creates a unique identifier for an agent.
func getAgentKey(roomName, serviceName string) string {
	return fmt.Sprintf("insights:%s_%s", roomName, serviceName)
}

// HandleIncomingAgentTask is the core logic that runs on every server.
func (s *InsightsModel) HandleIncomingAgentTask(payload *InsightsTaskPayload) {
	if payload.Task == TaskEnd {
		s.endLocalAgentTask(payload.ServiceName, payload.RoomName, payload.UserID)
		return
	} else if payload.Task == TaskEndRoomAgentByServiceName {
		s.removeAgentForRoom(payload.ServiceName, payload.RoomName)
		return
	} else if payload.Task == TaskEndRoomAllAgents {
		s.removeAgentsForRoom(payload.RoomName)
		return
	}

	// For both boot and start, we need to find or create the agent.
	// The leader election only happens on boot.
	if payload.Task == TaskBootAgent {
		lockKey := getAgentKey(payload.RoomName, payload.ServiceName)
		lock := s.redisService.NewLock(lockKey, 30*time.Second)

		isLeader, err := lock.TryLock(s.ctx)
		if err != nil {
			s.logger.WithError(err).Error("failed leader election attempt")
			return
		}

		if isLeader {
			s.logger.Infof("Acquired leadership for agent '%s'", lockKey)
			// We only create the agent here. The user task is not activated.
			if err := s.manageLocalAgent(payload, lock); err != nil {
				s.logger.WithError(err).Error("failed to manage local agent")
			}
		}
	} else if payload.Task == TaskStart {
		// No leader election needed. The agent should already be running.
		// We just find it and activate the user's task.
		key := getAgentKey(payload.RoomName, payload.ServiceName)
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if !ok {
			s.logger.Warnf("received a start task for a non-running agent: %s", key)
			// Optional: We could try to boot it here as a fallback.
			// For now, we'll just log a warning.
			return
		}
		err := agent.ActivateTaskForUser(payload.UserID, payload.Options)
		if err != nil {
			s.logger.WithError(err).Errorf("failed to activate task for user %s", payload.UserID)
		}
	}
}

// manageLocalAgent now only creates the agent. The user activation is separate.
func (s *InsightsModel) manageLocalAgent(payload *InsightsTaskPayload, lock *redisservice.Lock) error {
	key := getAgentKey(payload.RoomName, payload.ServiceName)

	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.roomAgents[key]; ok {
		// Agent already exists, nothing to do.
		return nil
	}

	s.logger.Infof("no agent found for service '%s' in room %s, creating a new one", payload.ServiceName, payload.RoomName)

	// Use the new helper method to get both configs
	targetAccount, serviceConfig, err := s.conf.Insights.GetProviderAccountForService(payload.ServiceName)
	if err != nil {
		_ = lock.Unlock(s.ctx)
		return err
	}

	agent, err := insightsservice.NewRoomAgent(s.ctx, s.conf, serviceConfig, targetAccount, s.logger, payload.RoomName, payload.ServiceName, payload.RoomE2EEKey)
	if err != nil {
		_ = lock.Unlock(s.ctx)
		return fmt.Errorf("failed to create insights agent: %w", err)
	}
	s.roomAgents[key] = agent

	go s.superviseAgent(agent, lock)
	return nil
}

// superviseAgent is the "Janitor" that maintains leadership.
func (s *InsightsModel) superviseAgent(agent *insightsservice.RoomAgent, lock *redisservice.Lock) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	s.logger.Infof("Supervisor started for agent '%s' in room '%s'", agent.ServiceName, agent.Room.Name())
	key := getAgentKey(agent.Room.Name(), agent.ServiceName)

	for {
		select {
		case <-ticker.C:
			if err := lock.Refresh(s.ctx); err != nil {
				s.logger.Warnf("Lost leadership for agent '%s', shutting down.", agent.ServiceName)
				s.shutdownAndRemoveAgent(key)
				return
			}
		case <-agent.Ctx.Done():
			s.logger.Infof("Agent for '%s' has shut down, releasing leadership.", agent.ServiceName)
			_ = lock.Unlock(s.ctx)
			s.shutdownAndRemoveAgent(key)
			return
		}
	}
}

// endLocalAgentTask is the internal method for the leader to use.
func (s *InsightsModel) endLocalAgentTask(serviceName, roomName, userId string) {
	key := getAgentKey(roomName, serviceName)
	s.lock.RLock()
	agent, ok := s.roomAgents[key]
	s.lock.RUnlock()

	if ok {
		agent.EndTasksForUser(userId)
	}
}

// shutdownAndRemoveAgent is the internal method that safely shuts down and removes a single agent.
func (s *InsightsModel) shutdownAndRemoveAgent(key string) {
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

func (s *InsightsModel) removeAgentForRoom(serviceName, roomName string) {
	key := getAgentKey(roomName, serviceName)
	s.shutdownAndRemoveAgent(key)
}

// removeAgentsForRoom now uses the new shutdownAndRemoveAgent method.
func (s *InsightsModel) removeAgentsForRoom(roomName string) {
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
