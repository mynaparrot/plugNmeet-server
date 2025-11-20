package models

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// getAgentKey creates a unique identifier for an agent using the new robust format.
// NEW FORMAT: {roomId}@{serviceName}
func getAgentKey(roomName, serviceName string) string {
	return fmt.Sprintf("%s@%s", roomName, serviceName)
}

// parseAgentKey safely extracts the roomId and serviceName from the new key format.
func parseAgentKey(key string) (roomId, serviceName string, err error) {
	parts := strings.SplitN(key, "@", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid agent key format: expected 'roomId@serviceName', got '%s'", key)
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid agent key format: empty roomId or serviceName in key '%s'", key)
	}
	return parts[0], parts[1], nil
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

	if payload.Task == TaskConfigureAgent {
		key := getAgentKey(payload.RoomName, payload.ServiceName)

		// 1. Check if we are the leader and the agent is already running locally.
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if ok {
			// We are the leader. This is an UPDATE request.
			s.logger.Infof("updating configuration for running agent: %s", key)
			agent.UpdateAllowedUsers(payload.TargetUsers)
			return // The update is done.
		}

		// 2. If no local agent, this is a BOOT request.
		// ADD JITTER: Wait for a random period to avoid a thundering herd on Redis.
		time.Sleep(time.Duration(rand.Intn(250)) * time.Millisecond)

		// Now, try to become the leader.
		lock := s.redisService.NewLock(key, 30*time.Second)
		isLeader, err := lock.TryLock(s.ctx)
		if err != nil {
			s.logger.WithError(err).Error("failed leader election attempt")
			return
		}

		if isLeader {
			s.logger.Infof("Acquired leadership for agent '%s'", key)
			// Create the agent...
			if err := s.manageLocalAgent(payload, lock); err != nil {
				s.logger.WithError(err).Error("failed to manage local agent")
				return
			}

			s.lock.RLock()
			newAgent, _ := s.roomAgents[key]
			s.lock.RUnlock()
			newAgent.UpdateAllowedUsers(payload.TargetUsers)
		}

	} else if payload.Task == TaskStart {
		key := getAgentKey(payload.RoomName, payload.ServiceName)
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if !ok {
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
	s.updateRoomMetadata(payload.RoomName, payload.ServiceName, true)
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

		roomId, serviceName, err := parseAgentKey(key)
		if err != nil {
			s.logger.WithError(err).Error("could not parse agent key during shutdown")
			return
		}
		s.updateRoomMetadata(roomId, serviceName, false)
	}
}

func (s *InsightsModel) removeAgentForRoom(serviceName, roomName string) {
	s.logger.Infof("removing agent for service '%s' in room '%s'", serviceName, roomName)
	key := getAgentKey(roomName, serviceName)
	s.shutdownAndRemoveAgent(key)
}

// removeAgentsForRoom now uses the new shutdownAndRemoveAgent method.
func (s *InsightsModel) removeAgentsForRoom(roomName string) {
	s.lock.RLock()
	// Find all agents that belong to this room without holding a write lock for the whole loop.
	keysToDelete := make([]string, 0)
	for key := range s.roomAgents {
		if strings.HasPrefix(key, roomName+"@") {
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

func (s *InsightsModel) updateRoomMetadata(roomId, serviceName string, enabled bool) {
	metadataStruct, err := s.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		s.logger.WithError(err).Error("failed to get room metadata")
		return
	}
	needToUpdate := false

	switch insights.ServiceType(serviceName) {
	case insights.ServiceTypeTranscription:
		if metadataStruct.RoomFeatures.InsightsFeatures != nil {
			metadataStruct.RoomFeatures.InsightsFeatures.TranscriptionFeatures.IsEnabled = enabled
			needToUpdate = true
		}
	case insights.ServiceTypeTranslation:
		if metadataStruct.RoomFeatures.InsightsFeatures != nil {
			metadataStruct.RoomFeatures.InsightsFeatures.ChatTranslationFeatures.IsEnabled = enabled
			needToUpdate = true
		}
	default:
		s.logger.Errorf("unknown insights service task: %s", serviceName)
	}

	if needToUpdate {
		err := s.natsService.UpdateAndBroadcastRoomMetadata(roomId, metadataStruct)
		if err != nil {
			s.logger.WithError(err).Error("failed to update room metadata")
		}
	}
}
