package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// Start activates the agent for the room. It is idempotent: it returns nil when
// the agent already runs, either on this server or on another one.
func (m *AIAgentModel) Start(ctx context.Context, roomId string, req *plugnmeet.AIAgentStartReq) error {
	log := m.logger.WithFields(logrus.Fields{"roomId": roomId, "method": "Start"})

	metadata, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		log.WithError(err).Error("failed to get room metadata")
		return config.ErrAIAgentGeneric
	}
	if metadata == nil {
		return config.ErrRoomNotFound
	}
	// the voice agent only ever runs inside a 1-on-1 session room, never in a
	// normal group room or in the parent lobby of per-user sessions
	if !isOneOnOneAIAgentRoom(metadata) {
		log.Warn("the ai agent can only be started in a 1-on-1 session room")
		return config.ErrAIAgentNotAllowed
	}

	insightsFeatures := metadata.GetRoomFeatures().GetInsightsFeatures()
	features := NormalizeAIAgentFeatures(insightsFeatures.GetAiAgentFeatures())
	if err := m.ValidateAIAgentFeatures(features, metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures(), insightsFeatures.GetIsAllow()); err != nil {
		log.WithError(err).Warn("ai agent activation refused")
		return err
	}
	if _, ok := insightsservice.GetActiveAIAgent(roomId); ok {
		return nil
	}

	// Leader election per room, shared with the rest of the insights machinery.
	redisLock := m.redisService.NewLock(aiAgentLockKey(roomId), aiAgentLockTTL)
	acquired, err := redisLock.TryLock(ctx)
	if err != nil {
		log.WithError(err).Error("leader election failed")
		return config.ErrAIAgentGeneric
	}
	if !acquired {
		log.Debug("another server hosts the ai agent for this room")
		return nil
	}
	started := false
	defer func() {
		if !started {
			_ = redisLock.Unlock(m.ctx)
		}
	}()

	roomInfo, err := m.natsService.GetRoomInfo(roomId)
	if err != nil || roomInfo == nil {
		log.WithError(err).Warn("room info is not available")
		return config.ErrRoomNotFound
	}
	account, serviceConfig, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)
	if err != nil {
		log.WithError(err).Error("ai_agent service is not configured")
		return config.ErrAIAgentProviderMissing
	}

	boundary := insightsservice.BoundaryConfigFromFeatures(features)
	if req.GetSystemPrompt() != "" {
		boundary.HostSystemPrompt = req.GetSystemPrompt()
	}
	if req.GetPreviousSessionContext() != "" {
		boundary.PreviousSessionSummary = req.GetPreviousSessionContext()
	}
	features.SystemPrompt = boundary.HostSystemPrompt
	features.PreviousSessionContext = &boundary.PreviousSessionSummary

	delegate, err := m.SelectOrGetDelegate(ctx, roomId)
	if err != nil {
		log.WithError(err).Warn("no delegate could be selected yet")
	}
	// Resolve the learner before the agent starts: the session telemetry carries
	// the identity, so the summary artifact still names them after they left.
	learner := aiAgentLearnerIdentity(m.aiAgentSessionLearner(ctx, roomId, nil))
	if learner.UserID == "" && delegate != "" {
		// The delegate is the participant the agent talks to, so it is the
		// learner even when the earlier lookups came back empty.
		learner = aiAgentLearnerIdentity(m.aiAgentParticipantInfo(roomId, delegate))
	}
	displayName := boundary.AgentDisplayName

	payload := &insights.InsightsTaskPayload{
		Task:                         TaskConfigureAgent,
		ServiceType:                  insights.ServiceTypeAIAgent,
		RoomId:                       roomId,
		RoomTableId:                  roomInfo.GetDbTableId(),
		UserId:                       delegate,
		RoomE2EEKey:                  metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures().EncryptionKey,
		AgentName:                    &displayName,
		HiddenAgent:                  false,
		CaptureAllParticipantsTracks: true,
	}
	// a 1-on-1 room listens to its named participants only, so a supervisor
	// joining to observe is never fed to the agent
	if allowed := aiAgentAllowedParticipantIds(features); len(allowed) > 0 {
		payload.TargetUsers = make(map[string]bool, len(allowed))
		for _, id := range allowed {
			payload.TargetUsers[id] = true
		}
	}

	deps := m.BuildToolDependencies(roomId, delegate, log)
	// The voice agent owns the ai_agent-scoped external MCP servers; it may
	// also use the shared ones. The text chat's servers stay invisible here.
	deps.MCPScope = mcp.MCPScopeAIAgent

	agentUserId := fmt.Sprintf("%s%s-%s", config.AgentUserUserIdPrefix, insights.ServiceTypeAIAgent, uuid.NewString())
	// Register the agent as a room participant before it joins LiveKit: browsers
	// resolve agent tracks through the NATS-backed participant list, so the entry
	// must exist by the time TrackSubscribed fires.
	if _, err := m.natsService.AddUserManuallyAndBroadcast(roomId, agentUserId, displayName, true, true); err != nil {
		log.WithError(err).Error("could not register the ai agent as a room participant")
		return config.ErrAIAgentGeneric
	}

	agent, err := insightsservice.NewAIAgent(&insightsservice.AIAgentArgs{
		Ctx:             m.ctx,
		AppConf:         m.appConfig,
		AgentUserId:     agentUserId,
		NatsConn:        m.natsConn,
		Transport:       m.natsService,
		NatsService:     m.natsService,
		RedisService:    m.redisService,
		ServiceConfig:   serviceConfig,
		ProviderAccount: account,
		Logger:          log,
		Payload:         payload,
		Features:        features,
		Boundary:        boundary,
		Deps:            deps,
		Learner:         learner,
		Delegate: func() (string, error) {
			return m.SelectOrGetDelegate(m.ctx, roomId)
		},
		OnStopped: func(reason string) {
			m.afterAgentStopped(roomId, reason)
		},
	})
	if err != nil {
		log.WithError(err).Error("failed to start the ai agent")
		m.removeAgentParticipant(roomId, agentUserId, displayName)
		return config.ErrAIAgentGeneric
	}
	started = true

	m.storeState(roomId, agent)
	m.AcquirePresenterForAgent(m.ctx, roomId, agentUserId)
	go m.superviseAgent(agent, redisLock)

	log.WithField("agentUserId", agent.AgentUserID()).Info("ai agent started")
	return nil
}

// Stop ends the room's agent session. It is a no-op when this server does not
// host the agent.
func (m *AIAgentModel) Stop(ctx context.Context, roomId, reason string) error {
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return nil
	}
	agent.Stop(reason)
	return nil
}

// Status reports the agent state, from the local agent when this server hosts it
// and from the shared Redis state otherwise.
func (m *AIAgentModel) Status(ctx context.Context, roomId string) (*plugnmeet.AIAgentStatusRes, error) {
	if agent, ok := insightsservice.GetActiveAIAgent(roomId); ok {
		return agent.Status(), nil
	}

	state, err := m.readState(ctx, roomId)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return &plugnmeet.AIAgentStatusRes{
			Status: false,
			Msg:    config.ErrAIAgentNotRunning.Error(),
		}, nil
	}

	return &plugnmeet.AIAgentStatusRes{
		Status: true,
		Msg:    config.MsgSuccess,
		State: &plugnmeet.AIAgentStateMsg{
			SessionId:   state.SessionId,
			AgentUserId: state.AgentUserId,
			AgentName:   state.AgentName,
			State:       state.State,
			StartedAt:   state.StartedAt,
			Reason:      state.Reason,
		},
	}, nil
}

// SendContext injects a host-side event into the live session.
func (m *AIAgentModel) SendContext(ctx context.Context, roomId string, req *plugnmeet.AIAgentSendContextReq) error {
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return config.ErrAIAgentNotRunning
	}
	if strings.TrimSpace(req.GetText()) == "" {
		return config.ErrAIAgentInvalidRequest
	}

	return agent.SendContext(req.GetText(), req.GetSource())
}

// Pause stops the agent's audio in and out but keeps the provider session.
func (m *AIAgentModel) Pause(ctx context.Context, roomId string) error {
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return config.ErrAIAgentNotRunning
	}
	return agent.Pause()
}

// Resume re-enables the agent's audio after a pause.
func (m *AIAgentModel) Resume(ctx context.Context, roomId string) error {
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return config.ErrAIAgentNotRunning
	}
	return agent.Resume()
}

// HandleClientToolResult resolves the tool call the answer belongs to. A call
// delegated outside an agent session (the admin AI text chat) is answered
// directly; anything else goes to the running agent's delegate.
func (m *AIAgentModel) HandleClientToolResult(ctx context.Context, roomId, userId string, msg *plugnmeet.AIAgentToolResultMsg) {
	if msg == nil {
		return
	}
	if pending := m.takePendingClientCall(msg.GetCallId(), roomId, userId); pending != nil {
		pending.ch <- msg
		return
	}
	if !m.IsUserAgentDelegate(ctx, roomId, userId) {
		return
	}
	if agent, ok := insightsservice.GetActiveAIAgent(roomId); ok {
		agent.HandleClientToolResult(msg)
	}
}

// HandleWhiteboardScene forwards a canvas snapshot to the running agent.
func (m *AIAgentModel) HandleWhiteboardScene(ctx context.Context, roomId, userId string, msg *plugnmeet.AIAgentWhiteboardSceneMsg) {
	if msg == nil {
		return
	}
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return
	}
	if !m.IsUserAgentDelegate(ctx, roomId, userId) && !m.natsService.IsUserPresenter(roomId, userId) {
		return
	}
	if msg.GetRoomId() == "" {
		msg.RoomId = roomId
	}
	if err := agent.HandleWhiteboardScene(msg); err != nil {
		m.logger.WithError(err).Debug("failed to hand the whiteboard scene to the ai agent")
	}
}

func (m *AIAgentModel) storeState(roomId string, agent *insightsservice.AIAgent) {
	state := &aiAgentRoomState{
		SessionId:   agent.SessionID(),
		AgentUserId: agent.AgentUserID(),
		AgentName:   agent.AgentName(),
		StartedAt:   time.Now().UnixMilli(),
		State:       plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING,
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		m.logger.WithError(err).Error("could not encode the ai agent state")
		return
	}
	if err := m.rds.Set(m.ctx, aiAgentKey(roomId, aiAgentKeyState), encoded, 0).Err(); err != nil {
		m.logger.WithError(err).Warn("could not store the ai agent state")
	}
}

// superviseAgent keeps the leadership lock fresh and releases it on teardown.
func (m *AIAgentModel) superviseAgent(agent *insightsservice.AIAgent, lock *redisservice.Lock) {
	ticker := time.NewTicker(aiAgentLockRefresh)
	defer ticker.Stop()

	for {
		select {
		case <-agent.Done():
			if err := lock.Unlock(m.ctx); err != nil {
				m.logger.WithError(err).Warn("could not release the ai agent lock")
			}
			return
		case <-ticker.C:
			if err := lock.Refresh(m.ctx); err != nil {
				m.logger.WithError(err).Warn("lost the ai agent leadership, stopping")
				agent.Stop("leadership_lost")
			}
		}
	}
}

// afterAgentStopped cleans the room's session state once the agent has stopped.
func (m *AIAgentModel) afterAgentStopped(roomId, reason string) {
	m.logger.WithFields(logrus.Fields{"roomId": roomId, "reason": reason}).Info("ai agent stopped")
	m.cancelStopTimer(roomId)

	ctx, cancel := context.WithTimeout(m.ctx, aiAgentCleanupTimeout)
	defer cancel()

	if state, err := m.readState(ctx, roomId); err == nil && state != nil {
		m.removeAgentParticipant(roomId, state.AgentUserId, state.AgentName)
	}
	m.RestorePreviousPresenter(ctx, roomId)
	// The external MCP configuration and its warmed tools stay in Redis: a stop
	// is often temporary (pause, empty-room grace) and the same room may restart
	// the agent. OnRoomEnded removes them together with the rest of the room.
	// The learner identity is already in the session telemetry, so dropping the
	// delegate here cannot strand the summary that follows the stop.
	_ = m.rds.Del(ctx,
		aiAgentKey(roomId, aiAgentKeyState),
		aiAgentKey(roomId, aiAgentKeyDelegate),
	).Err()

	// a 1-on-1 breakout room exists only for its session
	if reason == aiAgentStopReasonEmptyRoom || reason == aiAgentStopReasonBudget {
		m.closeBreakoutRoomAfterStop(roomId, reason)
	}
}

// closeBreakoutRoomAfterStop saves the session summary of a breakout room and
// then ends the room, so its session slot is freed and the room disappears from
// the parent's list of active breakout rooms.
func (m *AIAgentModel) closeBreakoutRoomAfterStop(roomId, reason string) {
	if m.bkRoomModel == nil {
		return
	}
	info, meta, err := m.natsService.GetRoomInfoWithMetadata(roomId)
	if err != nil || info == nil || meta == nil || !meta.GetIsBreakoutRoom() {
		return
	}
	parentRoomId, _, err := helpers.ParseBreakoutParentRoomRef(meta.GetParentRoomId())
	if err != nil {
		m.logger.WithError(err).WithField("roomId", roomId).Warn("could not resolve the parent of the breakout room")
		return
	}

	log := m.logger.WithFields(logrus.Fields{"roomId": roomId, "parentRoomId": parentRoomId, "reason": reason})
	go func() {
		ctx, cancel := context.WithTimeout(m.ctx, aiAgentRoomCloseTimeout)
		defer cancel()

		// the artifact is built from the room state, so it must come first
		m.FinalizeSession(ctx, info.GetDbTableId(), roomId, info.GetRoomSid())

		if err := m.bkRoomModel.EndBreakoutRoom(ctx, &plugnmeet.EndBreakoutRoomReq{RoomId: parentRoomId, BreakoutRoomId: roomId}); err != nil {
			log.WithError(err).Warn("could not end the breakout room of the stopped session")
		}
	}()
}

// readState returns the room's stored agent session state, or nil when none exists.
func (m *AIAgentModel) readState(ctx context.Context, roomId string) (*aiAgentRoomState, error) {
	raw, err := m.rds.Get(ctx, aiAgentKey(roomId, aiAgentKeyState)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	state := new(aiAgentRoomState)
	if err := json.Unmarshal(raw, state); err != nil {
		return nil, err
	}
	return state, nil
}

// removeAgentParticipant takes the agent out of the room's participant list.
func (m *AIAgentModel) removeAgentParticipant(roomId, agentUserId, agentName string) {
	if agentUserId == "" {
		return
	}
	m.natsService.DeleteUser(roomId, agentUserId)
	info := &plugnmeet.NatsKvUserInfo{UserId: agentUserId, Name: agentName, RoomId: roomId}
	if err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_USER_OFFLINE, roomId, info, nil); err != nil {
		m.logger.WithError(err).Warn("could not broadcast the ai agent offline event")
	}
}
