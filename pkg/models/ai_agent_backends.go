package models

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// aiAgentPollBackend exposes PollModel to the MCP poll tools.
type aiAgentPollBackend struct {
	m *AIAgentModel
}

func (b aiAgentPollBackend) CreatePoll(ctx context.Context, roomID, question string, options []string) (string, error) {
	if err := b.ensurePollsActive(roomID); err != nil {
		return "", err
	}

	opts := make([]*plugnmeet.CreatePollOptions, 0, len(options))
	for i, text := range options {
		opts = append(opts, &plugnmeet.CreatePollOptions{Id: uint32(i + 1), Text: text})
	}

	return b.m.pollModel.CreatePoll(&plugnmeet.CreatePollReq{
		RoomId:   roomID,
		UserId:   fmt.Sprintf("%s%s", config.AgentUserUserIdPrefix, insights.ServiceTypeAIAgent),
		Question: question,
		Options:  opts,
	})
}

func (b aiAgentPollBackend) ClosePoll(ctx context.Context, roomID, pollID string) error {
	return b.m.pollModel.ClosePoll(&plugnmeet.ClosePollReq{
		RoomId: roomID,
		PollId: pollID,
		UserId: fmt.Sprintf("%s%s", config.AgentUserUserIdPrefix, insights.ServiceTypeAIAgent),
	})
}

// CreateQuiz opens a native quiz poll: the correct option is flagged before the
// poll is broadcast, so the board can mark the answer key when it closes.
func (b aiAgentPollBackend) CreateQuiz(ctx context.Context, roomID, question string, options []string, correctOptionIndex int) (string, error) {
	if err := b.ensurePollsActive(roomID); err != nil {
		return "", err
	}

	opts := make([]*plugnmeet.CreatePollOptions, 0, len(options))
	for i, text := range options {
		opts = append(opts, &plugnmeet.CreatePollOptions{
			Id:        uint32(i + 1),
			Text:      text,
			IsCorrect: i == correctOptionIndex,
		})
	}

	return b.m.pollModel.CreatePoll(&plugnmeet.CreatePollReq{
		RoomId:   roomID,
		UserId:   fmt.Sprintf("%s%s", config.AgentUserUserIdPrefix, insights.ServiceTypeAIAgent),
		Question: question,
		Options:  opts,
		IsQuiz:   true,
	})
}

// ensurePollsActive turns the room's polls feature on when the host left it
// off, so a poll or quiz created by the agent is actually visible to the
// participants (the Polls icon and panel are gated by is_active).
func (b aiAgentPollBackend) ensurePollsActive(roomID string) error {
	if b.m == nil || b.m.natsService == nil || b.m.pollModel == nil {
		return nil
	}

	meta, err := b.m.natsService.GetRoomMetadataStruct(roomID)
	if meta == nil {
		// A missing room (e.g. a unit test without NATS metadata) must not
		// block the poll: there is no feature switch to flip.
		if err == nil {
			return nil
		}
		return err
	}

	return ensurePollsFeatureActive(roomID, meta, b.m.pollModel.ManageActivation)
}

// ensurePollsFeatureActive applies the polls activation rule to one room
// metadata snapshot. It is split from ensurePollsActive so the rule can be
// tested without a NATS room.
func ensurePollsFeatureActive(roomID string, meta *plugnmeet.RoomMetadata, activate func(*plugnmeet.ActivatePollsReq) error) error {
	pf := meta.GetRoomFeatures().GetPollsFeatures()
	if pf == nil || !pf.GetIsAllow() {
		return errors.New("polls are disabled in this room's configuration")
	}
	if pf.GetIsActive() {
		return nil
	}

	return activate(&plugnmeet.ActivatePollsReq{RoomId: roomID, IsActive: true})
}

// GetPollResults lists the room's polls when pollID is empty and returns one
// poll's detailed results otherwise. Aggregate counts only: the individual
// respondents never reach the model.
func (b aiAgentPollBackend) GetPollResults(_ context.Context, roomID, pollID string) (any, error) {
	if b.m.pollModel == nil || b.m.pollModel.rs == nil {
		return nil, errors.New("the poll store is not available")
	}
	rs := b.m.pollModel.rs

	if pollID == "" {
		polls, err := rs.GetRoomPolls(roomID)
		if err != nil {
			return nil, err
		}
		results := make([]mcp.PollResult, 0, len(polls))
		for _, info := range polls {
			result, err := pollResult(rs, roomID, info)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		}
		return results, nil
	}

	info, err := rs.GetPollInfoByPollId(roomID, pollID)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("no poll with that id exists in this room")
	}
	return pollResult(rs, roomID, info)
}

// pollResult flattens one poll and its aggregate results for the model.
func pollResult(rs *redisservice.RedisService, roomID string, info *plugnmeet.PollInfo) (mcp.PollResult, error) {
	result := mcp.PollResult{
		PollID:       info.GetId(),
		Question:     info.GetQuestion(),
		IsQuiz:       info.GetIsQuiz(),
		IsRunning:    info.GetIsRunning(),
		IsAnonymous:  info.GetIsAnonymous(),
		IsTextAnswer: info.GetIsTextAnswer(),
	}
	if info.GetId() == "" {
		return result, nil
	}

	details, err := rs.GetPollResponsesDetails(roomID, info.GetId(), info)
	if err != nil {
		return mcp.PollResult{}, err
	}
	result.TotalRespondents = details.GetTotalRespondents()

	votes := make(map[uint64]uint64, len(details.GetOptions()))
	for _, option := range details.GetOptions() {
		votes[option.GetId()] = option.GetVoteCount()
	}
	for _, option := range info.GetOptions() {
		result.Options = append(result.Options, mcp.PollResultOption{
			ID:        option.GetId(),
			Text:      option.GetText(),
			IsCorrect: option.GetIsCorrect(),
			Votes:     votes[uint64(option.GetId())],
		})
	}
	for _, answer := range details.GetTextAnswers() {
		result.TextAnswers = append(result.TextAnswers, mcp.PollResultText{
			Text:  answer.GetText(),
			Count: answer.GetCount(),
		})
	}

	return result, nil
}

// aiAgentChatBackend delivers the agent's written messages to the AI Chat box,
// privately when a target user is given and room-wide otherwise.
type aiAgentChatBackend struct {
	m *AIAgentModel
}

func (b aiAgentChatBackend) SendChatMessage(_ context.Context, roomID, toUserID, msg string) error {
	res := &plugnmeet.InsightsAITextChatStreamResult{
		Id:          uuid.NewString(),
		Text:        msg,
		IsLastChunk: true,
		CreatedAt:   fmt.Sprintf("%d", time.Now().UnixMilli()),
		RequestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_AI_AGENT.Enum(),
	}
	encoded, err := protojson.Marshal(res)
	if err != nil {
		return err
	}

	var target *string
	if toUserID = strings.TrimSpace(toUserID); toUserID != "" {
		target = &toUserID
	}

	return b.m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_RESP_INSIGHTS_AI_TEXT_CHAT, roomID, string(encoded), target)
}

// Lock-setting service names of the workspace surfaces the agent may unlock.
const (
	workspaceWhiteboard    = "whiteboard"
	workspaceSharedNotepad = "sharedNotepad"
)

// workspaceLocked reports whether a surface is write-locked. An unset user lock
// falls back to roomDefault; an unset default counts as locked, matching
// plugnmeet's locked-by-default rooms.
func workspaceLocked(user, roomDefault *plugnmeet.LockSettings, service string) bool {
	switch service {
	case workspaceWhiteboard:
		if user != nil && user.LockWhiteboard != nil {
			return user.GetLockWhiteboard()
		}
		return roomDefault == nil || roomDefault.LockWhiteboard == nil || roomDefault.GetLockWhiteboard()
	case workspaceSharedNotepad:
		if user != nil && user.LockSharedNotepad != nil {
			return user.GetLockSharedNotepad()
		}
		return roomDefault == nil || roomDefault.LockSharedNotepad == nil || roomDefault.GetLockSharedNotepad()
	}

	return false
}

// setWorkspaceLockValue writes one surface's lock value into l.
func setWorkspaceLockValue(l *plugnmeet.LockSettings, service string, locked bool) {
	if setter, ok := lockSettingMap[service]; ok {
		setter(l, &locked)
	}
}

// workspaceLockServices maps a set_workspace_lock surface to lock services.
func workspaceLockServices(surface string) ([]string, error) {
	switch surface {
	case workspaceWhiteboard:
		return []string{workspaceWhiteboard}, nil
	case "shared_notepad", workspaceSharedNotepad:
		return []string{workspaceSharedNotepad}, nil
	case "both":
		return []string{workspaceWhiteboard, workspaceSharedNotepad}, nil
	}

	return nil, fmt.Errorf("surface must be whiteboard, shared_notepad or both")
}

// setWorkspaceLockForRoom locks or unlocks a surface for the room default and
// every online participant. Only real changes are broadcast, so repeated tool
// calls stay silent once the room is already unlocked.
func (m *AIAgentModel) setWorkspaceLockForRoom(roomID, service string, locked bool) error {
	meta, err := m.natsService.GetRoomMetadataStruct(roomID)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("room not found")
	}

	if workspaceLocked(nil, meta.GetDefaultLockSettings(), service) != locked {
		if meta.DefaultLockSettings == nil {
			meta.DefaultLockSettings = &plugnmeet.LockSettings{}
		}
		setWorkspaceLockValue(meta.DefaultLockSettings, service, locked)
		if err := m.natsService.UpdateAndBroadcastRoomMetadata(roomID, meta); err != nil {
			return err
		}
	}

	userIDs, err := m.natsService.GetOnlineUsersId(roomID)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		if userID == "" || config.IsUserIdInternal(userID) {
			continue
		}
		if err := m.setWorkspaceLockForUser(roomID, userID, service, locked); err != nil {
			// One participant's metadata must not fail the whole tool call.
			m.logger.WithError(err).WithField("user_id", userID).
				Warnln("failed to update workspace lock for participant")
		}
	}

	return nil
}

// setWorkspaceLockForUser locks or unlocks a surface for a single participant.
func (m *AIAgentModel) setWorkspaceLockForUser(roomID, userID, service string, locked bool) error {
	userMeta, err := m.natsService.GetUserMetadataStruct(roomID, userID)
	if err != nil {
		return err
	}
	if userMeta == nil {
		return errors.New("the participant is not in this room")
	}
	if workspaceLocked(userMeta.GetLockSettings(), nil, service) == locked {
		return nil
	}

	if userMeta.LockSettings == nil {
		userMeta.LockSettings = &plugnmeet.LockSettings{}
	}
	setWorkspaceLockValue(userMeta.LockSettings, service, locked)

	return m.natsService.UpdateAndBroadcastUserMetadata(roomID, userID, userMeta, nil)
}

// aiAgentNotepadBackend opens the room's shared notepad for the agent's writes.
type aiAgentNotepadBackend struct {
	m *AIAgentModel
}

func (b aiAgentNotepadBackend) EnsureNotepadActive(_ context.Context, roomID string) error {
	meta, err := b.m.natsService.GetRoomMetadataStruct(roomID)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("room not found")
	}

	np := meta.GetRoomFeatures().GetSharedNotePadFeatures()
	if np == nil || !np.GetIsAllow() {
		return errors.New("the shared notepad is disabled in this room's configuration")
	}

	changed := false
	if np.NotePadId == "" {
		np.NotePadId = uuid.NewString()
		changed = true
	}
	if !np.GetIsActive() {
		np.IsActive = true
		changed = true
	}
	if !np.GetVisible() {
		np.Visible = true
		changed = true
	}
	if changed {
		if err := b.m.natsService.UpdateAndBroadcastRoomMetadata(roomID, meta); err != nil {
			return err
		}
	}

	// Writing needs an unlocked notepad, not just a visible one.
	return b.m.setWorkspaceLockForRoom(roomID, workspaceSharedNotepad, false)
}

// aiAgentWhiteboardBackend brings the whiteboard into view, hiding the floating
// notepad so it never covers the drawing.
type aiAgentWhiteboardBackend struct {
	m *AIAgentModel
}

func (b aiAgentWhiteboardBackend) EnsureWhiteboardVisible(_ context.Context, roomID string) error {
	meta, err := b.m.natsService.GetRoomMetadataStruct(roomID)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("room not found")
	}

	wb := meta.GetRoomFeatures().GetWhiteboardFeatures()
	if wb == nil || !wb.GetIsAllow() {
		return errors.New("the whiteboard is disabled in this room's configuration")
	}

	changed := false
	if !wb.GetVisible() {
		wb.Visible = true
		changed = true
	}
	if np := meta.GetRoomFeatures().GetSharedNotePadFeatures(); np != nil && np.GetVisible() {
		np.Visible = false
		changed = true
	}
	if changed {
		if err := b.m.natsService.UpdateAndBroadcastRoomMetadata(roomID, meta); err != nil {
			return err
		}
	}

	// Drawing needs an unlocked board, not just a visible one.
	return b.m.setWorkspaceLockForRoom(roomID, workspaceWhiteboard, false)
}

// taskError turns a room task's status code and i18n error key into a message
// the model can act on, so a tool result never surfaces a bare translation key.
func taskError(code plugnmeet.StatusCode, err error) error {
	if err == nil {
		return nil
	}

	switch code {
	case plugnmeet.StatusCode_PERMISSION_DENIED:
		return errors.New("this feature is not allowed in this room")
	case plugnmeet.StatusCode_ROOM_NOT_FOUND:
		return errors.New("the room is not active")
	case plugnmeet.StatusCode_USER_NOT_FOUND:
		return errors.New("the participant is not in this room")
	case plugnmeet.StatusCode_INVALID_PARAMETERS:
		return fmt.Errorf("the request does not match the current room state: %s", err.Error())
	}

	return err
}

// aiAgentBreakoutBackend drives the room's breakout rooms for the AI agent.
type aiAgentBreakoutBackend struct {
	m           *AIAgentModel
	actorUserID string
}

// CreateBreakoutRooms creates one child room per title and distributes the
// online, non-admin participants round-robin across them. Admins stay in the
// main room, and so do the internal agents (the AI agent itself).
func (b aiAgentBreakoutBackend) CreateBreakoutRooms(ctx context.Context, roomID, requestedUserID string, durationMinutes uint64, welcomeMsg string, titles []string) (int, error) {
	if len(titles) == 0 {
		return 0, errors.New("at least one breakout room title is required")
	}
	if requestedUserID == "" {
		requestedUserID = b.actorUserID
	}

	rooms := make([]*plugnmeet.BreakoutRoom, 0, len(titles))
	for i, title := range titles {
		rooms = append(rooms, &plugnmeet.BreakoutRoom{
			// The child room id is derived from this value, so it must be unique
			// per room and identical to the ids the admin UI produces.
			Id:       strconv.Itoa(i + 1),
			Title:    title,
			Duration: durationMinutes,
		})
	}

	var users []*plugnmeet.NatsKvUserInfo
	if b.m.natsService != nil {
		var err error
		users, err = b.m.natsService.GetOnlineUsersList(roomID)
		if err != nil {
			// Creating the rooms is still useful without the distribution.
			b.m.logger.WithError(err).Warnln("failed to list the online users for the breakout rooms")
			users = nil
		}
	}
	next := 0
	for _, user := range users {
		if user == nil || user.GetIsAdmin() || config.IsUserIdInternal(user.GetUserId()) {
			continue
		}
		room := rooms[next%len(rooms)]
		room.Users = append(room.Users, &plugnmeet.BreakoutRoomUser{
			Id:   user.GetUserId(),
			Name: user.GetName(),
		})
		next++
	}

	req := &plugnmeet.CreateBreakoutRoomsReq{
		RoomId:                roomID,
		RequestedUserId:       requestedUserID,
		Duration:              durationMinutes,
		Rooms:                 rooms,
		AllowReturnToMainRoom: true,
	}
	if welcomeMsg = strings.TrimSpace(welcomeMsg); welcomeMsg != "" {
		req.WelcomeMsg = &welcomeMsg
	}

	if err := b.m.submitClientAPI(ctx, roomID, requestedUserID, clientAPIRouteBreakoutCreate, req); err != nil {
		return 0, err
	}
	return len(rooms), nil
}

// ManageBreakoutRooms runs one of the breakout room actions. Only list returns a
// value: the room's breakout rooms with their participants.
func (b aiAgentBreakoutBackend) ManageBreakoutRooms(ctx context.Context, roomID, action, breakoutRoomID, message string, durationMinutes uint64) (any, error) {
	if b.m.bkRoomModel == nil {
		return nil, errors.New("the breakout rooms are not available right now")
	}

	switch action {
	case "list":
		return b.m.bkRoomModel.GetBreakoutRooms(roomID, b.actorUserID, true)
	case "broadcast_message":
		return nil, b.m.bkRoomModel.SendBreakoutRoomMsg(&plugnmeet.BroadcastBreakoutRoomMsgReq{RoomId: roomID, Msg: message})
	case "extend_duration":
		return nil, b.m.bkRoomModel.IncreaseBreakoutRoomDuration(&plugnmeet.IncreaseBreakoutRoomDurationReq{
			RoomId:         roomID,
			BreakoutRoomId: breakoutRoomID,
			Duration:       durationMinutes,
		})
	case "end_room":
		return nil, b.m.bkRoomModel.EndBreakoutRoom(ctx, &plugnmeet.EndBreakoutRoomReq{
			RoomId:         roomID,
			BreakoutRoomId: breakoutRoomID,
		})
	case "end_all":
		return nil, b.m.bkRoomModel.EndAllBreakoutRoomsByParentRoomId(ctx, roomID)
	}

	return nil, fmt.Errorf("unknown breakout room action %q", action)
}

// aiAgentInsightsBackend starts and stops the room's insights services. The
// model methods enforce that the host allowed the service and that no
// per-device E2EE key blocks it.
type aiAgentInsightsBackend struct {
	m           *AIAgentModel
	actorUserID string
}

func (b aiAgentInsightsBackend) ConfigureSpeechToText(ctx context.Context, roomID, action string, spokenLangs, transLangs, allowedUserIDs []string, enableSynthesis bool) error {
	switch action {
	case "stop":
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteTranscriptionEnd, nil)
	case "start_or_update":
		// The model may name the participants by name, by user id or ask for
		// everyone, so resolve the selection against the online participants.
		var users []*plugnmeet.NatsKvUserInfo
		if b.m.natsService != nil {
			online, err := b.m.natsService.GetOnlineUsersList(roomID)
			if err != nil && b.m.logger != nil {
				b.m.logger.WithError(err).Warnln("failed to list the online users for speech-to-text")
			}
			users = online
		}
		resolvedUsers := resolveSpeechUsers(allowedUserIDs, users)
		if len(resolvedUsers) == 0 {
			return errors.New("no matching participants found in the room for speech-to-text")
		}

		req := &plugnmeet.InsightsTranscriptionConfigReq{
			IsEnabled:                true,
			AllowedSpokenLangs:       spokenLangs,
			AllowedSpeechUsers:       resolvedUsers,
			IsEnabledTranslation:     len(transLangs) > 0,
			AllowedTransLangs:        transLangs,
			IsEnabledSpeechSynthesis: enableSynthesis && len(transLangs) > 0,
		}
		// The participants see the first spoken language first.
		if len(spokenLangs) > 0 {
			defaultLang := spokenLangs[0]
			req.DefaultSubtitleLang = &defaultLang
		}
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteTranscriptionConfigure, req)
	}

	return fmt.Errorf("action must be start_or_update or stop")
}

// resolveSpeechUsers turns the allowed_users selection of the model into the
// user ids the insights service can transcribe.
//
// Each entry may be a participant name, a user id, or "all"/"everyone" to
// allow every online human participant. Internal/bot users are never included.
// When the online list is unavailable (or empty, e.g. in a unit test) the raw
// user ids are kept so a plain user-id selection still works.
func resolveSpeechUsers(allowedUserIDs []string, users []*plugnmeet.NatsKvUserInfo) []string {
	if len(allowedUserIDs) == 0 {
		return nil
	}

	humans := make([]*plugnmeet.NatsKvUserInfo, 0, len(users))
	for _, user := range users {
		if isSpeechToTextBot(user.GetUserId()) {
			continue
		}
		humans = append(humans, user)
	}

	resolved := make([]string, 0, len(allowedUserIDs))
	seen := make(map[string]struct{}, len(allowedUserIDs))
	add := func(userID string) {
		if userID == "" {
			return
		}
		if _, ok := seen[userID]; ok {
			return
		}
		seen[userID] = struct{}{}
		resolved = append(resolved, userID)
	}

	for _, entry := range allowedUserIDs {
		if isEveryoneSpeechUser(entry) {
			for _, user := range humans {
				add(user.GetUserId())
			}
			return resolved
		}
	}

	for _, entry := range allowedUserIDs {
		entry = strings.TrimSpace(entry)
		if entry == "" || isEveryoneSpeechUser(entry) {
			continue
		}
		matched := false
		for _, user := range humans {
			if user.GetUserId() == entry || strings.EqualFold(strings.TrimSpace(user.GetName()), entry) {
				add(user.GetUserId())
				matched = true
			}
		}
		if !matched && len(humans) == 0 {
			add(entry)
		}
	}

	return resolved
}

// isEveryoneSpeechUser reports whether the entry selects all participants.
func isEveryoneSpeechUser(entry string) bool {
	switch strings.ToLower(strings.TrimSpace(entry)) {
	case "all", "everyone":
		return true
	}
	return false
}

// isSpeechToTextBot reports whether the user is an internal/bot participant
// whose audio must never be transcribed.
func isSpeechToTextBot(userID string) bool {
	return userID == "" || userID == config.RecorderBot || userID == config.RtmpBot || config.IsUserIdInternal(userID)
}

func (b aiAgentInsightsBackend) ConfigureChatTranslation(ctx context.Context, roomID, action string, transLangs []string, defaultLang string) error {
	switch action {
	case "stop":
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteChatTranslationEnd, nil)
	case "start_or_update":
		req := &plugnmeet.InsightsChatTranslationConfigReq{
			IsEnabled:         true,
			AllowedTransLangs: transLangs,
		}
		if defaultLang != "" {
			req.DefaultLang = &defaultLang
		}
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteChatTranslationConfigure, req)
	}

	return fmt.Errorf("action must be start_or_update or stop")
}

func (b aiAgentInsightsBackend) ConfigureMeetingSummarization(ctx context.Context, roomID, action, customPrompt string) error {
	switch action {
	case "stop":
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteMeetingSummarizationEnd, nil)
	case "start_or_update":
		return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteMeetingSummarizationConfigure, &plugnmeet.InsightsAIMeetingSummarizationConfigReq{
			IsEnabled:           true,
			SummarizationPrompt: customPrompt,
		})
	}

	return fmt.Errorf("action must be start_or_update or stop")
}

// aiAgentRoomOpsBackend drives the room-wide timer, the external media surfaces
// and the recording.
type aiAgentRoomOpsBackend struct {
	m           *AIAgentModel
	actorUserID string
}

func (b aiAgentRoomOpsBackend) actor(userID string) string {
	if userID = strings.TrimSpace(userID); userID != "" {
		return userID
	}
	return b.actorUserID
}

func (b aiAgentRoomOpsBackend) ManageCountdownTimer(_ context.Context, roomID, userID, action string, durationSeconds uint64) error {
	if b.m.roomModel == nil {
		return errors.New("the countdown timer is not available right now")
	}

	var task plugnmeet.CountdownTimerTask
	switch action {
	case "start":
		task = plugnmeet.CountdownTimerTask_START_TIMER
	case "pause":
		task = plugnmeet.CountdownTimerTask_PAUSE_TIMER
	case "resume":
		task = plugnmeet.CountdownTimerTask_RESUME_TIMER
	case "stop":
		task = plugnmeet.CountdownTimerTask_STOP_TIMER
	default:
		return fmt.Errorf("action must be start, pause, resume or stop")
	}

	// The timer tasks only apply to an active timer, and the host's toggle is
	// the room's own switch: starting through the agent turns it on first.
	if task == plugnmeet.CountdownTimerTask_START_TIMER {
		if err := b.activateCountdownTimer(roomID); err != nil {
			return err
		}
	}

	req := &plugnmeet.CountdownTimerReq{
		Task:   task,
		RoomId: roomID,
		UserId: b.actor(userID),
	}
	if durationSeconds > 0 {
		duration := uint32(durationSeconds)
		req.DurationSeconds = &duration
	}

	code, err := b.m.roomModel.HandleCountdownTimerTask(req)
	return taskError(code, err)
}

// activateCountdownTimer turns the room's countdown timer on when the host left
// it off, so a start command from the agent is not refused.
func (b aiAgentRoomOpsBackend) activateCountdownTimer(roomID string) error {
	meta, err := b.m.natsService.GetRoomMetadataStruct(roomID)
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("the room is not active")
	}
	cf := meta.GetRoomFeatures().GetCountdownTimerFeatures()
	if cf == nil || !cf.GetIsAllow() {
		return errors.New("the countdown timer is disabled in this room's configuration")
	}
	if cf.GetIsActive() {
		return nil
	}

	isActive := true
	code, err := b.m.roomModel.HandleCountdownTimerTask(&plugnmeet.CountdownTimerReq{
		Task:     plugnmeet.CountdownTimerTask_CHANGE_STATUS,
		IsActive: &isActive,
		RoomId:   roomID,
		UserId:   b.actorUserID,
	})
	return taskError(code, err)
}

func (b aiAgentRoomOpsBackend) ManageExternalMedia(_ context.Context, roomID, userID, action, mediaType, rawURL string) error {
	if b.m.roomModel == nil {
		return errors.New("the external media player is not available right now")
	}

	actor := b.actor(userID)
	rawURL = strings.TrimSpace(rawURL)
	if action != "start" && action != "stop" {
		return fmt.Errorf("action must be start or stop")
	}
	if action == "start" && rawURL == "" {
		return errors.New("url is required to start external media")
	}

	var code plugnmeet.StatusCode
	var err error
	switch mediaType {
	case "media_player":
		req := &plugnmeet.ExternalMediaPlayerReq{RoomId: roomID, UserId: actor}
		if action == "start" {
			req.Task = plugnmeet.ExternalMediaPlayerTask_START_PLAYBACK
			req.Url = &rawURL
		} else {
			req.Task = plugnmeet.ExternalMediaPlayerTask_END_PLAYBACK
		}
		code, err = b.m.roomModel.HandleExternalMediaTask(req)
	case "external_link":
		req := &plugnmeet.ExternalDisplayLinkReq{RoomId: roomID, UserId: actor}
		if action == "start" {
			req.Task = plugnmeet.ExternalDisplayLinkTask_START_EXTERNAL_LINK
			req.Url = &rawURL
		} else {
			req.Task = plugnmeet.ExternalDisplayLinkTask_STOP_EXTERNAL_LINK
		}
		code, err = b.m.roomModel.HandleExternalDisplayTask(req)
	default:
		return fmt.Errorf("media_type must be media_player or external_link")
	}

	return taskError(code, err)
}

func (b aiAgentRoomOpsBackend) ManageRecording(ctx context.Context, roomID, action string) error {
	var task plugnmeet.RecordingTasks
	switch action {
	case "start":
		task = plugnmeet.RecordingTasks_START_RECORDING
	case "stop":
		task = plugnmeet.RecordingTasks_STOP_RECORDING
	default:
		return fmt.Errorf("action must be start or stop")
	}

	var sid string
	if b.m.natsService != nil {
		roomInfo, err := b.m.natsService.GetRoomInfo(roomID)
		if err != nil {
			return err
		}
		if roomInfo == nil {
			return errors.New("the room is not active")
		}
		sid = roomInfo.GetRoomSid()
	}

	variant := plugnmeet.CloudRecordingVariants_FULL_SCREEN_CLOUD_RECORDING
	return b.m.submitClientAPI(ctx, roomID, b.actorUserID, clientAPIRouteRecording, &plugnmeet.RecordingReq{
		Task:             task,
		RoomId:           roomID,
		Sid:              sid,
		RecordingVariant: &variant,
	})
}

// aiAgentParticipantsBackend moderates the room's participants.
type aiAgentParticipantsBackend struct {
	m           *AIAgentModel
	actorUserID string
}

func (b aiAgentParticipantsBackend) ManageParticipants(ctx context.Context, roomID, requestedUserID, action, targetUserID, lockService string, locked bool) error {
	if b.m.userModel == nil {
		return errors.New("the participants are not available right now")
	}
	if requestedUserID == "" {
		requestedUserID = b.actorUserID
	}

	target := strings.TrimSpace(targetUserID)
	switch action {
	case "mute_microphone", "unmute_microphone":
		if target == "" {
			target = "all"
		}
		code, err := b.m.userModel.MuteUnMuteTrack(ctx, &plugnmeet.MuteUnMuteTrackReq{
			RoomId:          roomID,
			UserId:          target,
			Muted:           action == "mute_microphone",
			RequestedUserId: requestedUserID,
		})
		return taskError(code, err)

	case "switch_presenter":
		if target == "" || target == "all" {
			return errors.New("target_user_id of one participant is required to switch the presenter")
		}
		code, err := b.m.userModel.SwitchPresenter(&plugnmeet.SwitchPresenterReq{
			Task:            plugnmeet.SwitchPresenterTask_PROMOTE,
			RoomId:          roomID,
			UserId:          target,
			RequestedUserId: requestedUserID,
		})
		return taskError(code, err)

	case "approve_waiting_users":
		if target == "" || target == "all" {
			return errors.New("target_user_id of the waiting participant is required")
		}
		if b.m.roomModel == nil {
			return errors.New("the waiting room is not available right now")
		}
		code, err := b.m.roomModel.ApproveWaitingUsers(&plugnmeet.ApproveWaitingUsersReq{
			RoomId: roomID,
			UserId: target,
		})
		return taskError(code, err)

	case "update_lock_settings":
		if _, ok := lockSettingMap[lockService]; !ok {
			return fmt.Errorf("lock_service %q is not a lockable surface", lockService)
		}
		if target == "" {
			target = "all"
		}
		direction := "unlock"
		if locked {
			direction = "lock"
		}
		code, err := b.m.userModel.UpdateUserLockSettings(&plugnmeet.UpdateUserLockSettingsReq{
			RoomId:          roomID,
			UserId:          target,
			Service:         lockService,
			Direction:       direction,
			RequestedUserId: requestedUserID,
		})
		return taskError(code, err)
	}

	return fmt.Errorf("unknown participant action %q", action)
}

// aiAgentRoomBackend reads the read-only room state the tools may look at.
type aiAgentRoomBackend struct {
	m *AIAgentModel
}

func (b aiAgentRoomBackend) GetRoomSnapshot(ctx context.Context, roomID string) (*mcp.RoomSnapshot, error) {
	info, err := b.m.natsService.GetRoomInfo(roomID)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, errors.New("room not found")
	}

	snapshot := &mcp.RoomSnapshot{}
	var roomLocks *plugnmeet.LockSettings
	if metadata, err := b.m.natsService.GetRoomMetadataStruct(roomID); err == nil && metadata != nil {
		snapshot.RoomTitle = metadata.GetRoomTitle()
		roomLocks = metadata.GetDefaultLockSettings()
	}
	if users, err := b.m.natsService.GetOnlineUsersList(roomID); err == nil {
		for _, user := range users {
			if user == nil || config.IsUserIdInternal(user.GetUserId()) {
				continue
			}
			userMeta, _ := b.m.natsService.GetUserMetadataStruct(roomID, user.GetUserId())
			locks := userMeta.GetLockSettings()
			snapshot.Participants = append(snapshot.Participants, mcp.ParticipantSummary{
				UserID:              user.GetUserId(),
				Name:                user.GetName(),
				IsPresenter:         userMeta.GetIsPresenter(),
				IsAdmin:             user.GetIsAdmin(),
				WhiteboardLocked:    workspaceLocked(locks, roomLocks, workspaceWhiteboard),
				SharedNotepadLocked: workspaceLocked(locks, roomLocks, workspaceSharedNotepad),
			})
		}
	}
	if polls, err := b.m.pollModel.ListPolls(roomID); err == nil {
		for _, poll := range polls {
			if poll.GetIsRunning() {
				snapshot.ActivePollID = poll.GetId()
				break
			}
		}
	}

	return snapshot, nil
}

// SetWorkspaceLock locks or unlocks a surface for the whole room or for one
// participant. Unlocking also opens the surface, so the participant can draw or
// write as soon as the agent asks them to.
func (b aiAgentRoomBackend) SetWorkspaceLock(ctx context.Context, roomID, surface string, locked bool, userID string) error {
	services, err := workspaceLockServices(surface)
	if err != nil {
		return err
	}

	roomWide := userID == "" || userID == "all"
	for _, service := range services {
		if roomWide {
			err = b.m.setWorkspaceLockForRoom(roomID, service, locked)
		} else {
			err = b.m.setWorkspaceLockForUser(roomID, userID, service, locked)
		}
		if err != nil {
			return err
		}
	}

	if locked {
		return nil
	}

	for _, service := range services {
		switch service {
		case workspaceWhiteboard:
			err = (aiAgentWhiteboardBackend{m: b.m}).EnsureWhiteboardVisible(ctx, roomID)
		case workspaceSharedNotepad:
			err = (aiAgentNotepadBackend{m: b.m}).EnsureNotepadActive(ctx, roomID)
		}
		if err != nil {
			return err
		}
	}

	return nil
}
