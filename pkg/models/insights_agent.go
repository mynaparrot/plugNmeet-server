package models

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
)

// getAgentKey creates a unique identifier for an agent using the new robust format.
// NEW FORMAT: {roomId}@{serviceName}
func getAgentKey(roomName, serviceName string) string {
	return fmt.Sprintf("%s@%s", roomName, serviceName)
}

// HandleIncomingAgentTask is the core logic that runs on every server.
func (s *InsightsModel) HandleIncomingAgentTask(msg *nats.Msg) {
	payload := new(InsightsTaskPayload)
	err := json.Unmarshal(msg.Data, &payload)
	if err != nil {
		s.logger.WithError(err).Error("failed to unmarshal insights task payload")
		return
	}

	// Create a helper function to send the reply
	reply := func(status bool, message string) {
		if msg.Reply == "" {
			// Not a request, just a fire-and-forget message
			return
		}
		res := &AgentTaskResponse{Status: status, Msg: message}
		resBytes, _ := json.Marshal(res)
		err := s.conf.NatsConn.Publish(msg.Reply, resBytes)
		if err != nil {
			s.logger.WithError(err).Error("failed to publish reply")
		}
	}

	if payload.Task == TaskEnd {
		if ok := s.endLocalAgentTask(payload.ServiceName, payload.RoomName, payload.UserID); ok {
			reply(true, "agent task ended for user successfully")
		}
		return
	} else if payload.Task == TaskEndRoomAgentByServiceName {
		if ok := s.removeAgentForRoom(payload.ServiceName, payload.RoomName); ok {
			reply(true, "agent removed successfully")
		}
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
			reply(true, "agent configured successfully")
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
			reply(false, "failed leader election attempt")
			return
		}

		if isLeader {
			s.logger.Infof("Acquired leadership for agent '%s'", key)
			// Create the agent...
			if err := s.manageLocalAgent(payload, lock); err != nil {
				s.logger.WithError(err).Error("failed to manage local agent")
				reply(false, "failed to manage local agent")
				return
			}

			s.lock.RLock()
			newAgent, _ := s.roomAgents[key]
			s.lock.RUnlock()
			newAgent.UpdateAllowedUsers(payload.TargetUsers)
			reply(true, "agent configured successfully")
		}

	} else if payload.Task == TaskStart {
		key := getAgentKey(payload.RoomName, payload.ServiceName)
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if !ok {
			// not exist in this server
			return
		}

		err := agent.ActivateTaskForUser(payload.UserID, payload.Options)
		if err != nil {
			s.logger.WithError(err).Errorf("failed to activate task for user %s", payload.UserID)
			reply(false, "failed to manage local agent")
			return
		}
		reply(true, "agent configured successfully")
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
func (s *InsightsModel) endLocalAgentTask(serviceName, roomName, userId string) bool {
	key := getAgentKey(roomName, serviceName)
	s.lock.RLock()
	agent, ok := s.roomAgents[key]
	s.lock.RUnlock()

	if ok {
		agent.EndTasksForUser(userId)
	}
	return ok
}

// shutdownAndRemoveAgent is the internal method that safely shuts down and removes a single agent.
func (s *InsightsModel) shutdownAndRemoveAgent(key string) bool {
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
	return ok
}

func (s *InsightsModel) removeAgentForRoom(serviceName, roomName string) bool {
	s.logger.Infof("removing agent for service '%s' in room '%s'", serviceName, roomName)
	key := getAgentKey(roomName, serviceName)
	return s.shutdownAndRemoveAgent(key)
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
