package models

import (
	"encoding/json"
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

// ActivateAgentTask publishes a 'start' message to activate a room agent for a long-running task.
func (s *InsightsModel) ActivateAgentTask(serviceName, roomName, userId string, options []byte, roomE2EEKey *string) error {
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

// HandleIncomingAgentTask is the core logic that runs on every server.
func (s *InsightsModel) HandleIncomingAgentTask(payload *InsightsTaskPayload) {
	if payload.Task == TaskEnd {
		s.endLocalAgentTask(payload.ServiceName, payload.RoomName, payload.UserID)
		return
	} else if payload.Task == EndRoomTasks {
		s.removeAgentsForRoom(payload.RoomName)
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

// manageLocalAgent now uses the helper method.
func (s *InsightsModel) manageLocalAgent(payload *InsightsTaskPayload, lock *redisservice.Lock) error {
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

		agent, err = insightsservice.NewRoomAgent(s.ctx, s.conf, serviceConfig, targetAccount, s.logger, payload.RoomName, payload.ServiceName, payload.RoomE2EEKey)
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

// EndTask now only publishes an 'end' message.
func (s *InsightsModel) EndTask(serviceName, roomName, userId string) error {
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

// EndAllRoomTasks will close everything for this room
func (s *InsightsModel) EndAllRoomTasks(roomName string) error {
	s.logger.Infof("Publishing end all room tasks request for room '%s'", roomName)
	payload := &InsightsTaskPayload{
		Task: EndRoomTasks,
	}
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.conf.NatsConn.Publish(InsightsNatsChannel, p)
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
