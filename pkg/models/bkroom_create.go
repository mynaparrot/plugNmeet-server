package models

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	BreakoutRoomFormat = "%s-%s"
	// DefaultWhiteboardFileId is the whiteboard file id a room gets when no
	// office file is shared. Breakout rooms that don't inherit shared content
	// reset to this value so they start with a blank board, identical to a
	// normal room created without a file.
	DefaultWhiteboardFileId = ""
	// DefaultOfficeFileSentinel is the a-client store value for the built-in
	// blank whiteboard board (whiteboard slice's currentWhiteboardOfficeFileId
	// defaults to "default"). It is a valid breakout share target: it carries no
	// uploaded-file KV entry (unlike a real office file), so the create flow
	// seeds child rooms with file_id "default" and skips the room-file lookup.
	DefaultOfficeFileSentinel = "default"
	// aiBreakoutDefaultDurationMinutes is how long a per-user 1-on-1 AI room
	// lives when the parent room has no duration limit of its own.
	aiBreakoutDefaultDurationMinutes = 60
)

// effectiveMaxAISessions reports how many AI sessions a parent room may run in
// parallel: the configured max_concurrent_sessions of an open-per-user room,
// otherwise the server cap. allowed_number_rooms never applies here.
func effectiveMaxAISessions(meta *plugnmeet.RoomMetadata) uint32 {
	maxConc := aiAgentMaxConcurrentSessions(meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures())
	if maxConc == 0 {
		return utils.AIAgentMaxConcurrentSessionsCap
	}
	return clampUint32(maxConc, 1, utils.AIAgentMaxConcurrentSessionsCap)
}

func (m *BreakoutRoomModel) CreateBreakoutRooms(userCtx context.Context, r *plugnmeet.CreateBreakoutRoomsReq) ([]*plugnmeet.BreakoutRoom, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   r.RoomId,
		"method":   "CreateBreakoutRooms",
		"numRooms": len(r.Rooms),
	})
	log.Infoln("New request to create breakout rooms received")

	mainRoom, meta, err := m.natsService.GetRoomInfoWithMetadata(r.RoomId)
	if err != nil {
		log.WithError(err).Error("Failed to get parent room info")
		return nil, config.ErrBkRoomUnexpectedError
	}

	if mainRoom == nil || meta == nil {
		log.Error("invalid empty parent room information")
		return nil, config.ErrBkRoomUnexpectedError
	}

	// The parent's DB table id is required: it is the exact session reference
	// persisted with each breakout room (see meta.ParentRoomId below). Without
	// it we must not create children, otherwise their records would point to an
	// unresolvable parent.
	if mainRoom.DbTableId == 0 {
		log.Error("parent room is missing a DB table id")
		return nil, config.ErrBkRoomUnexpectedError
	}

	// do not allow nesting breakout rooms inside a breakout room.
	if meta.IsBreakoutRoom {
		log.Error("breakout rooms cannot be created inside another breakout room")
		return nil, config.ErrBkRoomCannotCreateInsideBreakout
	}

	// AI agent rooms are limited by the concurrent AI sessions of the parent,
	// regular breakout rooms by its allowed number of breakout rooms.
	if r.EnableAiAgent {
		if err := m.validateAIAgentBreakoutRequest(r, meta, log); err != nil {
			return nil, err
		}
	} else if meta.RoomFeatures != nil && meta.RoomFeatures.BreakoutRoomFeatures != nil {
		// AllowedNumberRooms == 0 means "not explicitly limited" (unlimited), so
		// we only reject when a positive limit is set and the request exceeds it.
		allowedRooms := meta.RoomFeatures.BreakoutRoomFeatures.AllowedNumberRooms
		if allowedRooms > 0 && uint32(len(r.Rooms)) > allowedRooms {
			log.WithField("allowedRooms", allowedRooms).Error("number of breakout rooms exceeds the allowed limit")
			return nil, config.ErrBkRoomMaxRoomsExceeded
		}
	}

	// validate cross-room entries: titles must be unique and a user may be
	// assigned to only one room.
	titles := make(map[string]struct{}, len(r.Rooms))
	assignedUsers := make(map[string]struct{})
	for _, room := range r.Rooms {
		if _, ok := titles[room.Title]; ok {
			log.WithField("title", room.Title).Error("duplicate breakout room title")
			return nil, config.ErrBkRoomDuplicateTitle
		}
		titles[room.Title] = struct{}{}
		for _, u := range room.Users {
			if _, ok := assignedUsers[u.Id]; ok {
				log.WithField("userId", u.Id).Error("user assigned to multiple breakout rooms")
				return nil, config.ErrBkRoomUserAssignedToMultipleRooms
			}
			assignedUsers[u.Id] = struct{}{}
		}
	}

	// let's check if the parent room has a duration set or not
	if meta.RoomFeatures.RoomDuration != nil && *meta.RoomFeatures.RoomDuration > 0 {
		if err := m.rm.CompareDurationWithParentRoom(r.RoomId, r.Duration); err != nil {
			log.WithError(err).Error("Duration comparison with parent room failed")
			return nil, config.ErrBkRoomDurationExceedsParent
		}
	}

	// auto-switch presenter because the later seeding flow needs presenter role.
	// This only applies to an explicit requester (a user-driven whiteboard
	// share). Requests without a requester (auto-created per-user AI rooms) seed
	// nothing and must never change the parent room's presenter.
	if r.RequestedUserId != "" && !m.natsService.IsUserPresenter(r.RoomId, r.RequestedUserId) {
		_, switchErr := m.um.SwitchPresenter(&plugnmeet.SwitchPresenterReq{
			Task:            plugnmeet.SwitchPresenterTask_PROMOTE,
			RoomId:          r.RoomId,
			UserId:          r.RequestedUserId,
			RequestedUserId: r.RequestedUserId,
		})
		if switchErr != nil {
			log.WithError(switchErr).Error("failed to make requesting user presenter for whiteboard share")
			return nil, config.ErrBkRoomUnexpectedError
		}
	}

	// set room duration
	meta.RoomFeatures.RoomDuration = &r.Duration
	meta.IsBreakoutRoom = true
	meta.WelcomeMessage = r.WelcomeMsg
	// the child's metadata carries the parent reference as
	// "<plain parent roomId>:<parent DB table id>" so metadata consumers can
	// resolve the exact parent session. All parent Redis keys, ids and
	// broadcasts keep using the plain r.RoomId.
	meta.ParentRoomId = helpers.FormatBreakoutParentRoomRef(r.RoomId, mainRoom.DbTableId)
	// this is crucial as breakout room should not generate userID
	meta.RoomFeatures.AutoGenUserId = new(false)

	// captured once, before any child is prepared: every child clones the parent
	// configuration, never an already-prepared sibling.
	parentAiFeatures := meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures()
	// only an auto per-user AI session turns a child into a 1-on-1 session room;
	// an admin-created AI breakout room of a normal session is a regular breakout
	isPerUserAISession := r.GetEnableAiAgent() && isAutoPerUserAISessionRoom(parentAiFeatures)

	// disable few features
	meta.RoomFeatures.WaitingRoomFeatures.IsActive = false
	applyBreakoutChildRoomFeatures(meta, r, isPerUserAISession)
	// we'll disable now. in the future, we can think about those
	meta.RoomFeatures.RecordingFeatures.IsAllow = false
	meta.RoomFeatures.ExternalBroadcastingFeatures.IsAllow = false
	meta.RoomFeatures.EnableAnalytics = false

	// force-disable meeting summarization for agent-based insights in breakout rooms
	if meta.RoomFeatures.InsightsFeatures != nil && meta.RoomFeatures.InsightsFeatures.AiFeatures != nil {
		meta.RoomFeatures.InsightsFeatures.AiFeatures.MeetingSummarizationFeatures = &plugnmeet.InsightsAIMeetingSummarizationFeatures{
			IsAllow:   false,
			IsEnabled: false,
		}
	}

	// a normal breakout room must never inherit an active AI agent; per-user AI
	// rooms get their own features below, one per room
	if !r.EnableAiAgent && meta.RoomFeatures.InsightsFeatures != nil && meta.RoomFeatures.InsightsFeatures.AiAgentFeatures != nil {
		meta.RoomFeatures.InsightsFeatures.AiAgentFeatures.IsAllow = false
	}

	// clear few main room data
	meta.RoomFeatures.DisplayExternalLinkFeatures.IsActive = false
	meta.RoomFeatures.ExternalMediaPlayerFeatures.IsActive = false
	meta.RoomFeatures.CountdownTimerFeatures = &plugnmeet.CountdownTimerFeatures{
		IsAllow: meta.RoomFeatures.CountdownTimerFeatures.GetIsAllow(),
	}

	// validate/normalize the optional whiteboard share request.
	_, childWbf, err := m.normalizeBreakoutWhiteboardShare(r, meta, log)
	if err != nil {
		log.WithError(err).Error("Failed to validate whiteboard share")
		return nil, config.ErrBkRoomUnexpectedError
	}
	meta.RoomFeatures.WhiteboardFeatures = childWbf

	// get all parent room files metadata into the breakout room bucket.
	parentFiles, _ := m.natsService.GetAllRoomFiles(r.RoomId)

	// copy the parent's poll definitions into the child room, when sharing.
	copiedPolls := make([]*plugnmeet.PollInfo, 0, len(r.PollShare.GetPollIds()))
	if r.PollShare != nil && len(r.PollShare.PollIds) > 0 {
		meta.RoomFeatures.PollsFeatures = &plugnmeet.PollsFeatures{
			IsAllow:  true,
			IsActive: true,
		}

		parentPolls, err := m.rs.GetRoomPolls(r.RoomId)
		if err != nil {
			log.WithError(err).Error("failed to fetch parent room polls for sharing")
			parentPolls = nil
		}

		selectedPollIds := make(map[string]struct{})
		for _, id := range r.PollShare.PollIds {
			selectedPollIds[id] = struct{}{}
		}

		for _, info := range parentPolls {
			if _, ok := selectedPollIds[info.GetId()]; !ok {
				continue
			}
			info.IsRunning = true
			copiedPolls = append(copiedPolls, info)
		}
	}

	e := make(map[string]bool)
	createdRooms := make([]*plugnmeet.BreakoutRoom, 0, len(r.Rooms))
	// creation-time snapshot of the pre-assignments that were actually created;
	// written to the parent's metadata below, superseding the API-seeded list.
	createdPreassigned := make([]*plugnmeet.PreassignedBreakoutRoom, 0, len(r.Rooms))

	for _, room := range r.Rooms {
		bRoomId := fmt.Sprintf(BreakoutRoomFormat, r.RoomId, room.Id)
		roomLog := log.WithFields(logrus.Fields{
			"breakoutRoomId":    bRoomId,
			"breakoutRoomTitle": room.Title,
		})

		bRoom := new(plugnmeet.CreateRoomReq)
		bRoom.RoomId = bRoomId
		meta.RoomTitle = room.Title
		if r.EnableAiAgent {
			prepareAIAgentBreakoutChild(parentAiFeatures, meta, room.Users[0].Id, r.GetAiAgentContext(), room.GetAiAgentContext())
		}
		bRoom.Metadata = meta
		// Step 6: capture the returned roomSid so it flows into the Redis hash,
		// the create response, and downstream listRooms/myRooms consumers.
		_, ari, err := m.rm.CreateRoom(userCtx, bRoom)

		if err != nil {
			roomLog.WithError(err).Error("Failed to create breakout room")
			e[bRoom.RoomId] = true
			continue
		}

		// store the canonical full breakout room id (<parentRoomId>-<n>) so the
		// stored proto and the Redis hash field agree; no client-side id rebuild needed.
		room.Id = bRoomId
		room.RoomSid = ari.GetSid()
		room.Duration = r.Duration
		room.Created = uint64(time.Now().Unix())
		// mark the room as an AI Agent session so listRooms/myRooms consumers
		// can show a badge without re-deriving it from the parent's metadata.
		room.EnableAiAgent = r.EnableAiAgent

		marshal, err := protojson.Marshal(room)
		if err != nil {
			roomLog.WithError(err).Error("Failed to marshal breakout room data")
			e[bRoom.RoomId] = true
			continue
		}

		if err := m.rs.InsertOrUpdateBreakoutRoom(r.RoomId, bRoom.RoomId, marshal); err != nil {
			roomLog.WithError(err).Error("Failed to insert breakout room in nats")
			e[bRoom.RoomId] = true
			continue
		}

		createdRooms = append(createdRooms, room)

		// snapshot this created room for the parent's metadata: its full id
		// (<parentRoomId>-<n>), title and the assigned user ids (may be empty).
		assignedUserIds := make([]string, 0, len(room.Users))
		for _, u := range room.Users {
			assignedUserIds = append(assignedUserIds, u.Id)
		}
		createdPreassigned = append(createdPreassigned, &plugnmeet.PreassignedBreakoutRoom{
			RoomId:  bRoomId,
			Title:   room.Title,
			UserIds: assignedUserIds,
		})

		// copy all parent room files metadata into the breakout room bucket.
		if parentFiles != nil {
			for _, pf := range parentFiles {
				if cErr := m.natsService.AddRoomFile(bRoomId, pf); cErr != nil {
					roomLog.WithError(cErr).Warn("failed to copy parent room file into breakout room")
				}
			}
		}

		if len(copiedPolls) > 0 {
			if cErr := m.rs.CreateRoomPolls(bRoomId, copiedPolls); cErr != nil {
				roomLog.WithError(cErr).Error("failed to write shared polls into breakout room")
			}
		}

		// now send invitation notification
		for _, u := range room.Users {
			err = m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_JOIN_BREAKOUT_ROOM, r.RoomId, bRoom.RoomId, &u.Id)
			if err != nil {
				roomLog.WithError(err).WithField("userId", u.Id).Error("Failed to send breakout room invitation")
				continue
			}
		}
	}

	if len(e) == len(r.Rooms) {
		log.Error("breakout room creation wasn't successful for any room")
		return nil, config.ErrBkRoomCreationFailed
	}

	// again here for update
	origMeta, err := m.natsService.UnmarshalRoomMetadata(mainRoom.Metadata)
	if err != nil {
		log.WithError(err).Error("Failed to unmarshal original parent room metadata")
		return createdRooms, config.ErrBkRoomUnexpectedError
	}
	origMeta.RoomFeatures.BreakoutRoomFeatures.IsActive = true
	// Auto-created per-user AI rooms have no requester and are additive: they are
	// merged by room id so that when the next participant joins, the previously
	// created (still active) rooms keep their preassignment, and they never touch
	// the host's own breakout preferences. Every other create (including an
	// admin's AI breakout rooms in a normal session) saves the create-time
	// snapshot and replaces the whole preassigned set.
	perUserCreate := r.RequestedUserId == "" || isPerUserAISession
	if !perUserCreate {
		origMeta.RoomFeatures.BreakoutRoomFeatures.AllowReturnToMainRoom = r.AllowReturnToMainRoom
		origMeta.RoomFeatures.BreakoutRoomFeatures.AllowSelfSelect = r.AllowSelfSelect
	}
	if perUserCreate {
		origMeta.RoomFeatures.BreakoutRoomFeatures.PreassignedRooms = mergePreassignedBreakoutRooms(origMeta.RoomFeatures.BreakoutRoomFeatures.GetPreassignedRooms(), createdPreassigned)
	} else {
		origMeta.RoomFeatures.BreakoutRoomFeatures.PreassignedRooms = createdPreassigned
	}

	if err := m.natsService.UpdateAndBroadcastRoomMetadata(r.RoomId, origMeta); err != nil {
		log.WithError(err).Error("Failed to update parent room metadata")
		return createdRooms, config.ErrBkRoomUnexpectedError
	}

	// send analytics
	m.analyticsModel.HandleEvent(&plugnmeet.AnalyticsDataMsg{
		EventType: plugnmeet.AnalyticsEventType_ANALYTICS_EVENT_TYPE_ROOM,
		EventName: plugnmeet.AnalyticsEvents_ANALYTICS_EVENT_ROOM_BREAKOUT_ROOM,
		RoomId:    r.RoomId,
	})

	log.Info("Finished breakout rooms creation")
	return createdRooms, err
}

// mergePreassignedBreakoutRooms returns existing plus created, keyed by room id:
// an entry from created supersedes an existing entry with the same room id while
// all other existing entries are preserved. This keeps earlier participants'
// preassigned rooms when per-user AI rooms are created incrementally.
func mergePreassignedBreakoutRooms(existing, created []*plugnmeet.PreassignedBreakoutRoom) []*plugnmeet.PreassignedBreakoutRoom {
	merged := make([]*plugnmeet.PreassignedBreakoutRoom, 0, len(existing)+len(created))
	index := make(map[string]int, len(existing)+len(created))

	for _, room := range existing {
		if room == nil {
			continue
		}
		if i, ok := index[room.GetRoomId()]; ok {
			merged[i] = room
			continue
		}
		index[room.GetRoomId()] = len(merged)
		merged = append(merged, room)
	}

	for _, room := range created {
		if room == nil {
			continue
		}
		if i, ok := index[room.GetRoomId()]; ok {
			merged[i] = room
			continue
		}
		index[room.GetRoomId()] = len(merged)
		merged = append(merged, room)
	}

	return merged
}

// validateAIAgentBreakoutRequest enforces what a 1-on-1 AI session needs: the
// parent must host the agent, every room carries exactly one assigned user and
// the new rooms must fit the parent's concurrent session limit.
func (m *BreakoutRoomModel) validateAIAgentBreakoutRequest(r *plugnmeet.CreateBreakoutRoomsReq, meta *plugnmeet.RoomMetadata, log *logrus.Entry) error {
	if !meta.GetRoomFeatures().GetInsightsFeatures().GetAiAgentFeatures().GetIsAllow() {
		log.Error("ai agent is not enabled for the parent room")
		return config.ErrAIAgentNotAllowed
	}

	// rooms that are still open already hold a session slot
	existing := int64(0)
	if count, err := m.rs.CountBreakoutRooms(r.RoomId); err == nil {
		existing = count
	}
	if maxSessions := effectiveMaxAISessions(meta); uint32(len(r.Rooms))+uint32(existing) > maxSessions {
		log.WithFields(logrus.Fields{"maxSessions": maxSessions, "existingRooms": existing}).Error("AI breakout rooms exceed the concurrent session limit")
		return config.ErrBkRoomMaxRoomsExceeded
	}

	// 1-on-1 means one assigned participant per room and no self-select
	if r.GetAllowSelfSelect() {
		log.Error("1-on-1 AI breakout rooms cannot be self-select")
		return config.ErrAIAgentBreakoutOneUser
	}
	for _, room := range r.Rooms {
		if len(room.GetUsers()) != 1 {
			log.WithFields(logrus.Fields{"breakoutRoom": room.GetTitle(), "users": len(room.GetUsers())}).Error("1-on-1 AI breakout room requires exactly 1 assigned user")
			return config.ErrAIAgentBreakoutOneUser
		}
	}

	return nil
}

// applyBreakoutChildRoomFeatures narrows the child metadata to what a breakout
// room may offer. Only an auto per-user 1-on-1 AI session room keeps the learner
// inside and keeps the parent's logout url; every other child (an admin-created
// breakout room of a normal session, with or without the AI agent) returns to
// the main room when it ends and therefore never redirects on its own.
func applyBreakoutChildRoomFeatures(meta *plugnmeet.RoomMetadata, r *plugnmeet.CreateBreakoutRoomsReq, isPerUserAISession bool) {
	features := &plugnmeet.BreakoutRoomFeatures{
		IsAllow:               false,
		AllowReturnToMainRoom: r.GetAllowReturnToMainRoom(),
		AllowSelfSelect:       r.GetAllowSelfSelect(),
		IsPerUserAiSession:    isPerUserAISession,
	}
	if r.GetEnableAiAgent() {
		// a 1-on-1 AI breakout room is created for exactly one participant
		features.AllowSelfSelect = false
	}
	if isPerUserAISession {
		features.AllowReturnToMainRoom = false
	} else {
		meta.LogoutUrl = nil
	}
	meta.RoomFeatures.BreakoutRoomFeatures = features
}

// prepareAIAgentBreakoutChild narrows the shared child metadata down to the one
// participant this 1-on-1 room is created for. parentAiFeatures is the parent
// room's configuration, captured before the create loop, so every child starts
// from it instead of inheriting the context of an earlier child.
func prepareAIAgentBreakoutChild(parentAiFeatures *plugnmeet.InsightsAIAgentFeatures, meta *plugnmeet.RoomMetadata, userId, globalContext, roomContext string) {
	insightsFeatures := meta.GetRoomFeatures().GetInsightsFeatures()
	if parentAiFeatures == nil || insightsFeatures == nil {
		return
	}

	// clone so every child room keeps its own participant list and prompt
	features := proto.Clone(parentAiFeatures).(*plugnmeet.InsightsAIAgentFeatures)
	features.SystemPrompt = appendBreakoutAIContext(features.GetSystemPrompt(), globalContext, roomContext)
	features.IsAllow = true
	features.IsEnabled = false
	features.ActiveSessionId = nil
	// the child is a controlled 1-on-1 room for exactly this participant, and it
	// keeps the auto-start preference of its parent session mode
	autoStart := true
	if parentAiFeatures.GetSessionMode() != plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_NORMAL_ROOM {
		autoStart = aiAgentAutoStart(parentAiFeatures)
	}
	features.SessionMode = plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS
	features.AutoSessionConfig = &plugnmeet.AIAgentAutoSessionConfig{
		AllowedParticipantIds: []string{userId},
		AutoStartAgent:        new(autoStart),
	}
	insightsFeatures.AiAgentFeatures = features
	applyAIAgentSessionRoomFeatures(meta)
}

// appendBreakoutAIContext preserves the parent's system prompt and appends the
// extra context of a 1-on-1 breakout session. Without any extra context the
// prompt is returned unchanged.
func appendBreakoutAIContext(basePrompt, globalContext, roomContext string) string {
	basePrompt = strings.TrimSpace(basePrompt)
	globalContext = strings.TrimSpace(globalContext)
	roomContext = strings.TrimSpace(roomContext)
	if globalContext == "" && roomContext == "" {
		return basePrompt
	}

	sessionContext := []string{"Breakout Session Context:"}
	if globalContext != "" {
		sessionContext = append(sessionContext, globalContext)
	}
	if roomContext != "" {
		sessionContext = append(sessionContext, roomContext)
	}

	section := strings.Join(sessionContext, "\n")
	if basePrompt == "" {
		return section
	}

	return basePrompt + "\n\n" + section
}

// EnsurePerUserAIBreakoutRoom returns the participant's own 1-on-1 AI breakout
// room, creating it on first call. The child room id is derived from the user id
// so repeated joins resolve to the same room.
func (m *BreakoutRoomModel) EnsurePerUserAIBreakoutRoom(ctx context.Context, parentRoomId, userId, userName string) (string, error) {
	childRoomId := fmt.Sprintf(BreakoutRoomFormat, parentRoomId, userId)
	log := m.logger.WithFields(logrus.Fields{
		"roomId":  parentRoomId,
		"userId":  userId,
		"childId": childRoomId,
		"method":  "EnsurePerUserAIBreakoutRoom",
	})

	_, meta, err := m.natsService.GetRoomInfoWithMetadata(parentRoomId)
	if err != nil {
		log.WithError(err).Error("failed to get parent room info")
		return "", config.ErrBkRoomUnexpectedError
	}
	if meta == nil {
		log.Error("invalid empty parent room metadata")
		return "", config.ErrBkRoomUnexpectedError
	}

	// an already open room is reused, so a re-join never creates a second one
	if room, err := m.fetchBreakoutRoom(parentRoomId, childRoomId); err == nil && room != nil {
		log.Info("per-user AI breakout room already exists")
		return childRoomId, nil
	}

	// no requester: the participant auto-joins through GetPNMJoinToken, which
	// neither seeds a parent whiteboard nor may promote anybody to presenter in
	// the parent room.
	_, err = m.CreateBreakoutRooms(ctx, &plugnmeet.CreateBreakoutRoomsReq{
		RoomId:          parentRoomId,
		RequestedUserId: "", // important as request isn't from client
		Duration:        m.perUserAIBreakoutDuration(parentRoomId),
		Rooms:           []*plugnmeet.BreakoutRoom{perUserAIBreakoutRoom(userId, userName)},
		// a learner of a 1-on-1 AI session never returns on their own
		AllowReturnToMainRoom: false,
		AllowSelfSelect:       false,
		EnableAiAgent:         true,
	})
	if err != nil {
		// every session slot is occupied right now, so the participant must be
		// told the room is full instead of receiving a generic breakout error
		if errors.Is(err, config.ErrBkRoomMaxRoomsExceeded) {
			log.WithError(err).Warn("no AI session slot is currently free")
			return "", config.ErrAIAgentSessionsFull
		}
		log.WithError(err).Error("failed to create the per-user AI breakout room")
		return "", err
	}

	log.Info("per-user AI breakout room created")
	return childRoomId, nil
}

// perUserAIBreakoutRoom builds the single 1-on-1 room of one participant.
func perUserAIBreakoutRoom(userId, userName string) *plugnmeet.BreakoutRoom {
	return &plugnmeet.BreakoutRoom{
		Id:    userId,
		Title: fmt.Sprintf("%s (AI Session)", userName),
		Users: []*plugnmeet.BreakoutRoomUser{
			{Id: userId, Name: userName},
		},
	}
}

// perUserAIBreakoutDuration returns the child's duration: the default, clamped
// to what is left of the parent room.
func (m *BreakoutRoomModel) perUserAIBreakoutDuration(parentRoomId string) uint64 {
	duration := uint64(aiBreakoutDefaultDurationMinutes)

	info, err := m.rm.GetRoomDurationInfo(parentRoomId)
	if err != nil || info == nil || info.Duration == 0 || info.StartedAt == 0 {
		return duration
	}

	now := uint64(time.Now().Unix())
	endsAt := info.StartedAt + info.Duration*60
	if endsAt <= now {
		return duration
	}
	remaining := (endsAt - now) / 60
	if remaining == 0 {
		remaining = 1
	}
	if remaining < duration {
		duration = remaining
	}

	return duration
}

// normalizeBreakoutWhiteboardShare validates the optional whiteboard share
// request and returns whether a share is active plus the WhiteboardFeatures the
// child rooms should use. Shared files are referenced by id (NOT copied): the
// parent's uploaded-file metadata supplies the path/pages, and the download
// endpoint serves those paths without a room-membership check. share_notepad is
// NOT handled here (later phase).
func (m *BreakoutRoomModel) normalizeBreakoutWhiteboardShare(r *plugnmeet.CreateBreakoutRoomsReq, meta *plugnmeet.RoomMetadata, log *logrus.Entry) (bool, *plugnmeet.WhiteboardFeatures, error) {
	parentWbf := meta.GetRoomFeatures().GetWhiteboardFeatures()
	childWbf := &plugnmeet.WhiteboardFeatures{
		IsAllow: parentWbf.GetIsAllow(),
	}

	share := r.GetWhiteboardShare()
	if share == nil {
		// No share requested: blank whiteboard (same defaults as a normal room
		// created without a file).
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	// Self-insert E2EE: each participant types their own secret locally and it
	// is cleared immediately so we'll never do cross sharing
	if meta.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetIsEnabled() &&
		meta.GetRoomFeatures().GetEndToEndEncryptionFeatures().GetEnabledSelfInsertEncryptionKey() {
		log.Infoln("whiteboard share ignored: self-insert E2EE is enabled")
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	if share.FileId == "" {
		log.Infoln("whiteboard share has no file id, treating as a blank whiteboard create")
		childWbf.WhiteboardFileId = DefaultWhiteboardFileId
		return false, childWbf, nil
	}

	// The built-in "default" board is a valid share target. It is the blank
	// board every room starts with and carries NO uploaded-file KV entry, so
	// there is nothing to look up in the parent's room-file bucket. Seed the
	// child directly with file_id "default" and the requested page range. A
	// default-board share counts as a validated share (presenter auto-switch +
	// content seeding) exactly like a real office file.
	if share.FileId == DefaultOfficeFileSentinel {
		// filePageCount=0 disables the per-file page clamp: the client already
		// scopes the selected pages to the default board's total, and the server
		// has no separate page-count source for the built-in board.
		pages := normalizeSelectedPages(share.Pages, 0)
		maxPage := uint32(0)
		for _, p := range pages {
			if p > maxPage {
				maxPage = p
			}
		}
		// A board always has at least one page; never leave the child with a
		// 0-page whiteboard when no explicit page was selected.
		if maxPage == 0 {
			maxPage = 1
		}
		// Clamp current_page into the selected range (same as the office path;
		// no persistence/response field for it yet — kept for the response/echo).
		if share.CurrentPage > maxPage {
			share.CurrentPage = maxPage
		}
		if share.CurrentPage < 1 {
			share.CurrentPage = 1
		}
		childWbf.WhiteboardFileId = DefaultOfficeFileSentinel
		childWbf.FileName = ""
		childWbf.FilePath = ""
		// The breakout shows only the shared page range, so total_pages is the
		// highest selected page (1 when nothing is explicitly selected).
		childWbf.TotalPages = maxPage
		return true, childWbf, nil
	}

	// The shared file must exist in the PARENT room's file KV bucket.
	fileMeta, err := m.natsService.GetRoomFile(r.RoomId, share.FileId)
	if err != nil {
		return false, nil, fmt.Errorf("failed to read shared whiteboard file from parent room: %w", err)
	}
	if fileMeta == nil {
		return false, nil, fmt.Errorf("shared whiteboard file %q not found in parent room", share.FileId)
	}

	// Normalize the requested page range: drop values < 1, dedupe, sort ascending,
	// and drop any page beyond the file's known page count. Keep ORIGINAL page
	// numbering (never remap indices).
	pages := normalizeSelectedPages(share.Pages, fileMeta.GetTotalPages())
	if len(pages) == 0 {
		// treat as all pages
		if fileMeta.TotalPages != nil && *fileMeta.TotalPages > 0 {
			pages = make([]uint32, *fileMeta.TotalPages)
			for i := range pages {
				pages[i] = uint32(i + 1)
			}
		}
	}

	maxPage := uint32(0)
	for _, p := range pages {
		if p > maxPage {
			maxPage = p
		}
	}
	if fileMeta.TotalPages != nil && *fileMeta.TotalPages > 0 && maxPage == 0 {
		maxPage = uint32(*fileMeta.TotalPages)
	}

	// A file always has at least one page; never leave the child with a 0-page
	// whiteboard (unknown page count + no explicit selection).
	if maxPage == 0 {
		maxPage = 1
	}

	// Clamp current_page into the selected range. There is no persistence/response
	// field for it yet (later seeding phase); just keep the normalized value
	// available on the request for the response/echo.
	if share.CurrentPage > maxPage {
		share.CurrentPage = maxPage
	}
	if share.CurrentPage < 1 {
		share.CurrentPage = 1
	}

	childWbf.WhiteboardFileId = fileMeta.FileId
	childWbf.FileName = fileMeta.FileName
	childWbf.FilePath = fileMeta.FilePath
	// The breakout shows only the shared page range, so total_pages is the highest
	// selected page (max page count if all pages are shared), NOT the parent's full total.
	childWbf.TotalPages = maxPage

	return true, childWbf, nil
}

// normalizeSelectedPages returns the requested page numbers cleaned up for use by
// a breakout room: values < 1 dropped, duplicates removed, sorted ascending, and
// any page beyond filePageCount (when > 0) dropped. Original page numbering is
// preserved (never remapped).
func normalizeSelectedPages(requested []uint32, filePageCount int32) []uint32 {
	seen := make(map[uint32]struct{})
	result := make([]uint32, 0, len(requested))
	for _, p := range requested {
		if p < 1 {
			continue
		}
		if filePageCount > 0 && int32(p) > filePageCount {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (m *BreakoutRoomModel) PostTaskAfterRoomStartWebhook(roomId string, metadata *plugnmeet.RoomMetadata) error {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":       roomId,
		"parentRoomId": metadata.ParentRoomId,
		"method":       "PostTaskAfterRoomStartWebhook",
	})
	log.Info("Handling post-start tasks for breakout room")

	// the metadata reference is "<parent roomId>:<parent table id>"; the plain
	// room id is required as the Redis hash key.
	parentRoomId, _, err := helpers.ParseBreakoutParentRoomRef(metadata.ParentRoomId)
	if err != nil {
		log.WithError(err).Error("Failed to parse parent room reference")
		return err
	}

	// now in livekit rooms are created almost instantly & sending webhook response
	// if this happened then we'll have to wait few seconds otherwise room info can't be found
	time.Sleep(config.WaitBeforeBreakoutRoomOnAfterRoomStart)

	room, err := m.fetchBreakoutRoom(parentRoomId, roomId)
	if err != nil {
		log.Error("Failed to fetch breakout room info")
		return err
	}
	room.Created = metadata.StartedAt
	room.Started = true

	marshal, err := protojson.Marshal(room)
	if err != nil {
		log.WithError(err).Error("Failed to marshal breakout room data")
		return err
	}

	err = m.rs.InsertOrUpdateBreakoutRoom(parentRoomId, roomId, marshal)
	if err != nil {
		log.WithError(err).Error("Failed to update breakout room info in nats")
		return err
	}

	log.Info("Successfully handled post-start tasks for breakout room")
	return nil
}
