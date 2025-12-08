package models

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// getAgentKey creates a unique identifier for an agent using the new robust format.
// NEW FORMAT: {roomId}@{serviceName}
func getAgentKey(roomName string, serviceType insights.ServiceType) string {
	return fmt.Sprintf("%s@%s", roomName, serviceType)
}

// parseAgentKey safely extracts the roomId and serviceName from the new key format.
func parseAgentKey(key string) (roomId string, serviceType insights.ServiceType, err error) {
	parts := strings.SplitN(key, "@", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid agent key format: expected 'roomId@serviceName', got '%s'", key)
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid agent key format: empty roomId or serviceName in key '%s'", key)
	}
	return parts[0], insights.ServiceType(parts[1]), nil
}

// HandleIncomingAgentTask is the core logic that runs on every server.
func (s *InsightsModel) HandleIncomingAgentTask(msg *nats.Msg) {
	payload := new(insights.InsightsTaskPayload)
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
		err := s.appConfig.NatsConn.Publish(msg.Reply, resBytes)
		if err != nil {
			s.logger.WithError(err).Error("failed to publish reply")
		}
	}

	if payload.Task == TaskUserEnd {
		if ok := s.endLocalAgentTask(payload.ServiceType, payload.RoomId, payload.UserId); ok {
			reply(true, "agent task ended for user successfully")
		}
		return
	} else if payload.Task == TaskEndRoomAgentByServiceName {
		if ok := s.removeAgentForRoom(payload.ServiceType, payload.RoomId); ok {
			reply(true, "agent removed successfully")
		}
		return
	} else if payload.Task == TaskEndRoomAllAgents {
		s.removeAgentsForRoom(payload.RoomId)
		return
	} else if payload.Task == TaskGetUserStatus {
		key := getAgentKey(payload.RoomId, payload.ServiceType)
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if ok { // This server is the leader
			serviceType, opts, isActive := agent.GetUserTaskOptions(payload.UserId)
			t, _ := insights.FromServiceType(serviceType)
			res := &plugnmeet.InsightsGetUserStatusRes{
				ServiceType: t,
				IsActive:    isActive,
			}
			switch serviceType {
			case insights.ServiceTypeTranscription:
				options := &insights.TranscriptionOptions{}
				if err := json.Unmarshal(opts, &options); err == nil {
					res.SpokenLang = &options.SpokenLang
					res.AllowedTranscriptionStorage = &options.AllowedTranscriptionStorage
				}
			}
			if marshal, err := proto.Marshal(res); err == nil {
				err := msg.Respond(marshal)
				if err != nil {
					s.logger.WithError(err).Error("failed to respond to user status request")
				}
			}
		}
		// Non-leaders simply ignore the request and do not reply.
		return
	}

	if payload.Task == TaskConfigureAgent {
		key := getAgentKey(payload.RoomId, payload.ServiceType)

		// 1. Check if we are the leader and the agent is already running locally.
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if ok {
			// We are the leader. This is an UPDATE request.
			s.logger.Infof("updating configuration for running agent: %s", key)
			if payload.CaptureAllParticipantsTracks {
				agent.ActivateRoomWideTask()
			} else {
				agent.UpdateAllowedUsers(payload.TargetUsers)
			}
			reply(true, "agent configured successfully")
			return // The update is done.
		}

		// 2. If no local agent, this is a BOOT request.
		// ADD JITTER: Wait for a random period to avoid a thundering herd on Redis.
		time.Sleep(time.Duration(rand.Intn(250)) * time.Millisecond)

		// Now, try to become the leader.
		redisLock := s.redisService.NewLock(key, 30*time.Second)
		isLeader, err := redisLock.TryLock(s.ctx)
		if err != nil {
			s.logger.WithError(err).Error("failed leader election attempt")
			reply(false, "failed leader election attempt")
			return
		}

		if isLeader {
			s.logger.Infof("Acquired leadership for agent '%s'", key)
			// Create the agent...
			if err := s.manageLocalAgent(payload, redisLock); err != nil {
				s.logger.WithError(err).Error("failed to manage local agent")
				reply(false, "failed to manage local agent")
				return
			}

			s.lock.RLock()
			newAgent, _ := s.roomAgents[key]
			s.lock.RUnlock()
			if payload.CaptureAllParticipantsTracks {
				newAgent.ActivateRoomWideTask()
			} else {
				newAgent.UpdateAllowedUsers(payload.TargetUsers)
			}
			reply(true, "agent configured successfully")
		}

	} else if payload.Task == TaskUserStart {
		key := getAgentKey(payload.RoomId, payload.ServiceType)
		s.lock.RLock()
		agent, ok := s.roomAgents[key]
		s.lock.RUnlock()

		if !ok {
			// not exist in this server
			return
		}

		err := agent.ActivateTaskForUser(payload.UserId, payload.Options)
		if err != nil {
			s.logger.WithError(err).Errorf("failed to activate task for user %s", payload.UserId)
			reply(false, "failed to manage local agent")
			return
		}
		reply(true, "agent configured successfully")
	}
}

// manageLocalAgent now only creates the agent. The user activation is separate.
func (s *InsightsModel) manageLocalAgent(payload *insights.InsightsTaskPayload, redisLock *redisservice.Lock) error {
	key := getAgentKey(payload.RoomId, payload.ServiceType)

	s.lock.Lock()
	defer s.lock.Unlock()

	if _, ok := s.roomAgents[key]; ok {
		// Agent already exists, nothing to do.
		return nil
	}

	s.logger.Infof("no agent found for service '%s' in room %s, creating a new one", payload.ServiceType, payload.RoomId)

	// Use the new helper method to get both configs
	targetAccount, serviceConfig, err := s.appConfig.Insights.GetProviderAccountForService(payload.ServiceType)
	if err != nil {
		_ = redisLock.Unlock(s.ctx)
		return err
	}

	agent, err := insightsservice.NewRoomAgent(s.ctx, s.appConfig, serviceConfig, targetAccount, s.natsService, s.redisService, s.logger, payload)
	if err != nil {
		_ = redisLock.Unlock(s.ctx)
		return fmt.Errorf("failed to create insights agent: %w", err)
	}
	s.roomAgents[key] = agent

	go s.superviseAgent(agent, redisLock)
	return nil
}

// superviseAgent is the "Janitor" that maintains leadership and health.
func (s *InsightsModel) superviseAgent(agent *insightsservice.RoomAgent, redisLock *redisservice.Lock) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	key := getAgentKey(agent.Room.Name(), agent.GetServiceType())
	s.logger.Infof("Supervisor started for agent '%s'", key)

	for {
		select {
		case <-ticker.C:
			if err := redisLock.Refresh(s.ctx); err != nil {
				s.logger.Warnf("Lost leadership for agent '%s', shutting down.", key)
				s.shutdownAndRemoveAgent(key)
				return
			}
		case <-agent.Ctx.Done():
			s.logger.Infof("Agent for '%s' context was canceled, shutting down.", key)
			_ = redisLock.Unlock(s.ctx)
			s.shutdownAndRemoveAgent(key)
			return
		}
	}
}

// endLocalAgentTask is the internal method for the leader to use.
func (s *InsightsModel) endLocalAgentTask(serviceType insights.ServiceType, roomName, userId string) bool {
	key := getAgentKey(roomName, serviceType)
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

		roomId, serviceType, err := parseAgentKey(key)
		if err != nil {
			s.logger.WithError(err).Error("could not parse agent key during shutdown")
			return ok
		}

		switch serviceType {
		case insights.ServiceTypeTranscription:
			_ = s.broadcastEndTranscription(roomId)
		}
	}
	return ok
}

func (s *InsightsModel) removeAgentForRoom(serviceType insights.ServiceType, roomName string) bool {
	s.logger.Infof("removing agent for service '%s' in room '%s'", serviceType, roomName)
	key := getAgentKey(roomName, serviceType)
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
