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

// aiAgentSendContextMaxRunes bounds a client-supplied context injection.
const aiAgentSendContextMaxRunes = 4000

// Bounds of one client-supplied whiteboard snapshot. The image is base64 encoded and sent to the provider,
// so an oversized frame would cost an unbounded amount of tokens.
const (
	aiAgentMaxWhiteboardImageBytes = 512 * 1024
	aiAgentMaxWhiteboardSceneBytes = 128 * 1024
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
	// A BYOK room stores its provider server-side, so the credentials are no
	// longer part of the features: the room-aware validation must accept the
	// stored provider in place of the server configuration.
	if err := m.ValidateRoomAIAgentFeatures(ctx, roomId, features, metadata.GetRoomFeatures().GetEndToEndEncryptionFeatures(), insightsFeatures.GetIsAllow()); err != nil {
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
	account, serviceConfig, isCustomProvider, err := m.resolveAIAgentProvider(ctx, roomId)
	if err != nil || account == nil {
		log.WithError(err).Error("ai_agent service is not configured")
		return config.ErrAIAgentProviderMissing
	}

	boundary := insightsservice.BoundaryConfigFromFeatures(features)
	if req.GetSystemPrompt() != "" {
		boundary.HostSystemPrompt = req.GetSystemPrompt()
	}
	features.SystemPrompt = boundary.HostSystemPrompt

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
	// The 1-on-1 voice agent only needs the countdown timer from the room-admin
	// tool set; breakout, insights, media/recording and participant moderation
	// belong to the administrator's AI text chat.
	deps.Breakout = nil
	deps.Insights = nil
	deps.Participants = nil
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

	// A custom provider session carries the provider name into the telemetry, so
	// the artifacts can break the usage down per provider; the platform provider
	// stays unnamed.
	customProvider := ""
	if isCustomProvider {
		customProvider = string(serviceConfig.Provider)
	}

	// A restart in the same room (reconnect, crash, or an explicit stop) continues
	// the earlier session instead of wiping its usage and conversation.
	prevTelemetry, err := m.redisService.GetAIAgentSessionTelemetry(ctx, roomId)
	if err != nil {
		log.WithError(err).Debug("could not read the previous ai agent session telemetry")
	}

	// The avatar is either fully resolvable or the session never gets one: a
	// half configured provider would only fail once the agent is already live.
	var avatar *insightsservice.AIAgentAvatarConfig
	if features.GetAvatar().GetIsEnabled() {
		provider, avatarID, apiKey, endpoint, authHeader, isCustomAvatar, err := m.resolveAIAgentAvatarConfig(ctx, roomId, features)
		if err != nil {
			log.WithError(err).Warn("the ai agent avatar could not be resolved")
			return err
		}
		avatar = &insightsservice.AIAgentAvatarConfig{
			Provider:   provider,
			AvatarID:   avatarID,
			APIKey:     apiKey,
			Endpoint:   endpoint,
			AuthHeader: authHeader,
			IsCustom:   isCustomAvatar,
			MaxMinutes: features.GetAvatar().GetMaxAvatarMinutes(),
		}
	}

	agent, err := insightsservice.NewAIAgent(&insightsservice.AIAgentArgs{
		Ctx:               m.ctx,
		AppConf:           m.appConfig,
		AgentUserId:       agentUserId,
		NatsConn:          m.natsConn,
		Transport:         m.natsService,
		NatsService:       m.natsService,
		RedisService:      m.redisService,
		ServiceConfig:     serviceConfig,
		ProviderAccount:   account,
		CustomProvider:    customProvider,
		Logger:            log,
		Payload:           payload,
		Features:          features,
		Boundary:          boundary,
		Deps:              deps,
		Learner:           learner,
		SummarizeModel:    m.aiAgentSummarizeModel(serviceConfig),
		PreviousTelemetry: prevTelemetry,
		Avatar:            avatar,
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
		if avatar != nil {
			// A failed start may already have brought the avatar bot into the
			// room, and it would keep publishing video on its own.
			m.removeAgentAvatarTwin(roomId, agentUserId)
		}
		return config.ErrAIAgentGeneric
	}
	started = true

	m.storeState(roomId, agent)
	m.AcquirePresenterForAgent(m.ctx, roomId, agentUserId)
	// The agent owns both surfaces for the session: the participant always starts
	// in view-only mode, even when the room was created with unlocked defaults.
	_ = m.setWorkspaceLockForRoom(roomId, workspaceWhiteboard, true)
	_ = m.setWorkspaceLockForRoom(roomId, workspaceSharedNotepad, true)
	go m.superviseAgent(agent, redisLock)

	log.WithField("agentUserId", agent.AgentUserID()).Info("ai agent started")
	return nil
}

// Stop ends the room's agent session. It is a no-op when this server does not
// host the agent.
func (m *AIAgentModel) Stop(ctx context.Context, roomId, reason string) error {
	if agent, ok := m.hostedAgent(roomId); ok {
		agent.Stop(reason)
	}
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
			SessionId:      state.SessionId,
			AgentUserId:    state.AgentUserId,
			AgentName:      state.AgentName,
			State:          state.State,
			StartedAt:      state.StartedAt,
			Reason:         state.Reason,
			AvatarProvider: optionalString(state.AvatarProvider),
		},
	}, nil
}

// SendContext injects a host-side event into the live session.
func (m *AIAgentModel) SendContext(ctx context.Context, roomId string, req *plugnmeet.AIAgentSendContextReq) error {
	agent, ok := insightsservice.GetActiveAIAgent(roomId)
	if !ok {
		return config.ErrAIAgentNotRunning
	}
	text := truncate(strings.TrimSpace(req.GetText()), aiAgentSendContextMaxRunes)
	if text == "" {
		return config.ErrAIAgentInvalidRequest
	}
	if strings.EqualFold(strings.TrimSpace(req.GetSource()), "countdown_timer") {
		return agent.SendProactiveContext(text, req.GetSource())
	}

	return agent.SendContext(text, req.GetSource())
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
		agent.HandleClientToolResultFromUser(userId, msg)
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
	if !m.sanitizeWhiteboardScene(roomId, msg) {
		return
	}
	if err := agent.HandleWhiteboardScene(msg); err != nil {
		m.logger.WithError(err).Debug("failed to hand the whiteboard scene to the ai agent")
	}
}

// sanitizeWhiteboardScene enforces the size limits and drops an image that is not
// a WebP frame. It reports whether the snapshot is still worth delivering.
func (m *AIAgentModel) sanitizeWhiteboardScene(roomId string, msg *plugnmeet.AIAgentWhiteboardSceneMsg) bool {
	if len(msg.GetSceneGraphJson()) > aiAgentMaxWhiteboardSceneBytes {
		m.logger.WithFields(logrus.Fields{
			"roomId": roomId,
			"bytes":  len(msg.GetSceneGraphJson()),
		}).Warn("dropping an oversized whiteboard scene graph")
		return false
	}
	if len(msg.GetWebpImage()) > aiAgentMaxWhiteboardImageBytes {
		m.logger.WithFields(logrus.Fields{
			"roomId": roomId,
			"bytes":  len(msg.GetWebpImage()),
		}).Warn("dropping an oversized whiteboard snapshot image")
		msg.WebpImage = nil
	}
	if img := msg.GetWebpImage(); len(img) > 0 && !isWebPImage(img) {
		m.logger.WithField("roomId", roomId).Warn("dropping a whiteboard snapshot image that is not webp")
		msg.WebpImage = nil
	}

	return len(msg.GetWebpImage()) > 0 || strings.TrimSpace(msg.GetSceneGraphJson()) != ""
}

// isWebPImage reports whether data starts with a RIFF/WEBP header.
func isWebPImage(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

func (m *AIAgentModel) storeState(roomId string, agent *insightsservice.AIAgent) {
	state := &aiAgentRoomState{
		SessionId:      agent.SessionID(),
		AgentUserId:    agent.AgentUserID(),
		AgentName:      agent.AgentName(),
		StartedAt:      time.Now().UnixMilli(),
		State:          plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING,
		AvatarProvider: agent.AvatarProvider(),
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
		if state.AvatarProvider != "" {
			m.removeAgentAvatarTwin(roomId, state.AgentUserId)
		}
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

	// Nothing drives the workspace anymore, so the participant gets it back. A
	// closing room has no participant left to unlock it for.
	if reason != aiAgentStopReasonEmptyRoom && reason != aiAgentStopReasonBudget {
		m.unlockWorkspace(roomId)
	}

	// a 1-on-1 breakout room exists only for its session
	if reason == aiAgentStopReasonEmptyRoom || reason == aiAgentStopReasonBudget {
		m.closeBreakoutRoomAfterStop(roomId, reason)
	}
}

// unlockWorkspace gives the whiteboard and the shared notepad back to the
// participant after the agent stopped.
func (m *AIAgentModel) unlockWorkspace(roomId string) {
	for _, service := range []string{workspaceWhiteboard, workspaceSharedNotepad} {
		if err := m.setWorkspaceLockForRoom(roomId, service, false); err != nil {
			m.logger.WithError(err).WithField("roomId", roomId).
				Warnln("failed to unlock the workspace after the ai agent stopped")
		}
	}
}

// closeBreakoutRoomAfterStop saves the session summary of a breakout room and
// then ends the room, so its session slot is freed and the room disappears from
// the parent's list of active breakout rooms. Only the 1-on-1 room of an auto
// per-user session exists solely for its session; an AI breakout room of a
// normal session is a regular breakout room, whose lifecycle belongs to its
// duration or to the host who created it.
func (m *AIAgentModel) closeBreakoutRoomAfterStop(roomId, reason string) {
	if m.closeBreakoutRoom != nil {
		m.closeBreakoutRoom(roomId, reason)
		return
	}
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

	// the parent decides: a normal session keeps its AI breakout rooms open
	parentMeta, err := m.natsService.GetRoomMetadataStruct(parentRoomId)
	if err != nil {
		log.WithError(err).Warn("could not get the parent room metadata, keeping the breakout room open")
		return
	}
	if !isAutoPerUserAISessionRoom(parentMeta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()) {
		log.Info("breakout room does not belong to an auto per-user ai session, keeping it open")
		return
	}

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

// IsAgentActive reports whether an agent session is running in the room, either
// on this server or on another one.
func (m *AIAgentModel) IsAgentActive(ctx context.Context, roomId string) bool {
	if _, ok := insightsservice.GetActiveAIAgent(roomId); ok {
		return true
	}
	state, err := m.readState(ctx, roomId)

	return err == nil && state != nil
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

// removeAgentAvatarTwin takes the avatar bot out of LiveKit. The bot joins as
// the agent's native twin, while shutting the room connection of the agent down
// only disconnects the agent itself: without this the avatar would keep
// publishing video to a room that no longer has an agent.
func (m *AIAgentModel) removeAgentAvatarTwin(roomId, agentUserId string) {
	if m.lk == nil || roomId == "" || agentUserId == "" {
		return
	}
	twinIdentity := config.GetNativeTwinIdentity(agentUserId)
	if twinIdentity == agentUserId {
		return
	}
	if _, err := m.lk.RemoveParticipant(roomId, twinIdentity); err != nil {
		m.logger.WithError(err).WithFields(logrus.Fields{
			"roomId":       roomId,
			"agentUserId":  agentUserId,
			"twinIdentity": twinIdentity,
		}).Debug("could not remove the ai agent avatar from livekit")
	}
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
