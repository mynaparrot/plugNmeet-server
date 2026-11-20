package models

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const (
	AIAgentSummarySourceLLM           = "llm"
	AIAgentSummarySourceLocalFallback = "local_fallback"

	// aiAgentSummaryBudget is the hard cap of the LLM synthesis attempt: a
	// missing artifact is worse than a slightly worse one.
	aiAgentSummaryBudget = 5 * time.Second

	aiAgentSummaryMaxTranscriptTurns = 80
	aiAgentSummaryMaxTranscriptRunes = 400
	aiAgentSummaryMaxContextRunes    = 1200
)

// FinalizeSession stops the room's agent, writes the session summary artifact
// and drops the Redis state the summary was built from. A room without any
// agent activity returns nil.
func (m *AIAgentModel) FinalizeSession(ctx context.Context, roomTableId uint64, roomId, roomSid string) *plugnmeet.AIAgentSessionSummary {
	if m == nil {
		return nil
	}
	log := m.logger.WithFields(logrus.Fields{"roomId": roomId, "roomSid": roomSid, "method": "FinalizeSession"})

	// The agent flushes its telemetry to Redis while stopping.
	m.StopForRoomEnd(ctx, roomId)

	cleanupCtx, cancel := context.WithTimeout(ctx, aiAgentCleanupTimeout)
	defer cancel()

	if m.redisService == nil {
		m.cleanupRoomState(cleanupCtx, roomId)
		return nil
	}

	telemetry, err := m.redisService.GetAIAgentSessionTelemetry(cleanupCtx, roomId)
	if err != nil {
		log.WithError(err).Debug("could not read the ai agent session telemetry")
	}
	progressMap, err := m.redisService.GetAILearningProgress(cleanupCtx, roomId)
	if err != nil {
		log.WithError(err).Debug("could not read the ai learning progress")
	}
	if telemetry == nil && len(progressMap) == 0 {
		m.cleanupRoomState(cleanupCtx, roomId)
		return nil
	}

	quizzes := collectQuizEvaluation(cleanupCtx, m.redisService, roomId)
	summary := buildDeterministicSessionSummary(roomId, roomSid, telemetry, sortedProgressEntries(progressMap), quizzes)

	// The learner identity is resolved once, so the artifact, the file metadata
	// and the usage artifact all carry the same participant.
	learner := m.aiAgentSessionLearner(cleanupCtx, roomId, telemetry)
	applyAIAgentSessionLearner(summary, learner)

	var promptTokens, completionTokens uint32
	summarizeModel := m.synthesizeSessionSummary(cleanupCtx, roomId, summary, telemetry, &promptTokens, &completionTokens, log)

	if m.artifactModel != nil && roomTableId > 0 {
		if _, err := m.artifactModel.CreateAIAgentSessionSummaryArtifact(roomTableId, roomId, roomSid, summary, telemetry, promptTokens, completionTokens, summarizeModel, m.logger); err != nil {
			log.WithError(err).Error("failed to create the ai agent session summary artifact")
		}
	}

	m.redisService.DeleteAIAgentSessionData(cleanupCtx, roomId)
	m.cleanupRoomState(cleanupCtx, roomId)
	return summary
}

// applyAIAgentSessionLearner fills the summary's top-level user_info, which is
// always present so the host can attribute the artifact. A missing learner
// leaves an empty object instead of a nil field.
func applyAIAgentSessionLearner(summary *plugnmeet.AIAgentSessionSummary, learner *plugnmeet.RoomArtifactUserInfo) {
	if summary == nil {
		return
	}
	if summary.UserInfo == nil {
		summary.UserInfo = &plugnmeet.RoomArtifactUserInfo{}
	}
	mergeAIAgentUserInfo(summary.UserInfo, learner)
}

// aiAgentLearnerIdentity converts an artifact identity into the session's
// learner, so a running agent can carry it into its telemetry.
func aiAgentLearnerIdentity(info *plugnmeet.RoomArtifactUserInfo) insightsservice.LearnerIdentity {
	if info == nil {
		return insightsservice.LearnerIdentity{}
	}
	return insightsservice.LearnerIdentity{
		UserID:    info.GetUserId(),
		UserName:  info.GetName(),
		ExUserID:  info.GetExUserId(),
		ExtraData: info.GetExtraData(),
	}
}

// aiAgentSessionLearner resolves the participant of a 1-on-1 AI session. The
// telemetry comes first: it is flushed while the agent runs, so the identity
// survives a participant leaving or a session restart.
func (m *AIAgentModel) aiAgentSessionLearner(ctx context.Context, roomId string, telemetry *redisservice.AIAgentSessionTelemetry) *plugnmeet.RoomArtifactUserInfo {
	info := &plugnmeet.RoomArtifactUserInfo{}
	if telemetry != nil {
		info.UserId = telemetry.UserID
		info.Name = optionalString(telemetry.UserName)
		info.ExUserId = optionalString(telemetry.ExUserID)
		info.ExtraData = optionalString(telemetry.ExtraData)
	}
	if info.UserId == "" {
		info.UserId = m.aiAgentLearnerUserId(ctx, roomId)
	}
	if info.UserId == "" {
		return info
	}

	mergeAIAgentUserInfo(info, m.aiAgentParticipantInfo(roomId, info.UserId))
	return info
}

// aiAgentLearnerUserId finds the single participant of a 1-on-1 session: the
// fixed roster of a controlled session, the current delegate, the only online
// human, or the user encoded in a per-user breakout room.
func (m *AIAgentModel) aiAgentLearnerUserId(ctx context.Context, roomId string) string {
	if m.natsService != nil {
		if allowed := aiAgentAllowedParticipantIds(m.roomAgentFeatures(roomId)); len(allowed) == 1 {
			return allowed[0]
		}
	}
	// The delegate key works without NATS and is the identity the agent talks to.
	if delegate, err := m.rds.Get(ctx, aiAgentKey(roomId, aiAgentKeyDelegate)).Result(); err == nil && delegate != "" {
		return delegate
	}
	if m.natsService == nil {
		return ""
	}
	if humans := m.onlineHumans(roomId); len(humans) > 0 {
		return humans[0].GetUserId()
	}
	// The room's participant bucket also keeps identities the online listing
	// missed, e.g. a user who disconnected right before the session finalized.
	known := make([]string, 0, 4)
	for _, id := range m.natsService.GetRoomUserIds(roomId) {
		if id != "" && !config.IsUserIdInternal(id) {
			known = append(known, id)
		}
	}
	if len(known) > 0 {
		sort.Strings(known)
		return known[0]
	}
	if parentRoomId := m.aiAgentParentRoomId(roomId); parentRoomId != "" {
		if userId := aiAgentBreakoutUserId(parentRoomId, roomId); userId != "" {
			return userId
		}
		if info := m.aiAgentBreakoutUserInfo(parentRoomId, roomId, ""); info.GetUserId() != "" {
			return info.GetUserId()
		}
	}
	return ""
}

// aiAgentParticipantInfo resolves the artifact identity of one participant: NATS
// knows them while online, the parent room of a per-user session knows them
// after they moved into their breakout room, and the breakout room record keeps
// the name once they left entirely.
func (m *AIAgentModel) aiAgentParticipantInfo(roomId, userId string) *plugnmeet.RoomArtifactUserInfo {
	info := &plugnmeet.RoomArtifactUserInfo{UserId: userId}
	if m.natsService == nil || userId == "" {
		return info
	}
	mergeAIAgentUserInfo(info, m.aiAgentNatsUserInfo(roomId, userId))
	if parentRoomId := m.aiAgentParentRoomId(roomId); parentRoomId != "" {
		mergeAIAgentUserInfo(info, m.aiAgentNatsUserInfo(parentRoomId, userId))
		mergeAIAgentUserInfo(info, m.aiAgentBreakoutUserInfo(parentRoomId, roomId, userId))
	}
	return info
}

// aiAgentParentRoomId reports the parent room of a breakout room, or "" when the
// room is no breakout room.
func (m *AIAgentModel) aiAgentParentRoomId(roomId string) string {
	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil || !meta.GetIsBreakoutRoom() {
		return ""
	}
	parentRoomId, _, err := helpers.ParseBreakoutParentRoomRef(meta.GetParentRoomId())
	if err != nil {
		return ""
	}
	return parentRoomId
}

// aiAgentNatsUserInfo reads the participant's identity from the room's NATS KV.
func (m *AIAgentModel) aiAgentNatsUserInfo(roomId, userId string) *plugnmeet.RoomArtifactUserInfo {
	info, err := m.natsService.GetUserInfo(roomId, userId)
	if err != nil || info.GetUserId() == "" {
		return nil
	}

	userInfo := &plugnmeet.RoomArtifactUserInfo{UserId: userId, Name: optionalString(info.GetName())}
	metadata, err := m.natsService.UnmarshalUserMetadata(info.GetMetadata())
	if err != nil || metadata == nil {
		return userInfo
	}
	userInfo.ExUserId = optionalString(metadata.GetExUserId())
	if extra := metadata.GetExtraData(); len(extra) > 0 {
		if encoded, merr := json.Marshal(extra); merr == nil {
			userInfo.ExtraData = optionalString(string(encoded))
		}
	}
	return userInfo
}

// aiAgentBreakoutUserInfo reads a participant from the breakout room record,
// which outlives the NATS entry of a participant who already left.
func (m *AIAgentModel) aiAgentBreakoutUserInfo(parentRoomId, roomId, userId string) *plugnmeet.RoomArtifactUserInfo {
	if m.redisService == nil {
		return nil
	}
	raw, err := m.redisService.GetBreakoutRoom(parentRoomId, roomId)
	if err != nil || raw == "" {
		return nil
	}
	room := new(plugnmeet.BreakoutRoom)
	if err = protojson.Unmarshal([]byte(raw), room); err != nil {
		return nil
	}
	for _, user := range room.GetUsers() {
		if user.GetId() == "" || (userId != "" && user.GetId() != userId) {
			continue
		}
		return &plugnmeet.RoomArtifactUserInfo{UserId: user.GetId(), Name: optionalString(user.GetName())}
	}
	return nil
}

// aiAgentBreakoutUserId decodes the per-user breakout room naming convention
// "<parent roomId>-<userId>". A numeric suffix is a numbered breakout room and
// therefore no user.
func aiAgentBreakoutUserId(parentRoomId, roomId string) string {
	prefix := fmt.Sprintf(BreakoutRoomFormat, parentRoomId, "")
	if !strings.HasPrefix(roomId, prefix) {
		return ""
	}
	userId := strings.TrimPrefix(roomId, prefix)
	if userId == "" {
		return ""
	}
	if _, err := strconv.ParseUint(userId, 10, 64); err == nil {
		return ""
	}
	return userId
}

// mergeAIAgentUserInfo fills the empty fields of dst from src.
func mergeAIAgentUserInfo(dst, src *plugnmeet.RoomArtifactUserInfo) {
	if dst == nil || src == nil {
		return
	}
	if dst.GetUserId() == "" {
		dst.UserId = src.GetUserId()
	}
	if dst.GetName() == "" {
		dst.Name = src.Name
	}
	if dst.GetExUserId() == "" {
		dst.ExUserId = src.ExUserId
	}
	if dst.GetExtraData() == "" {
		dst.ExtraData = src.ExtraData
	}
}

// synthesizeSessionSummary lets the configured model refine the deterministic
// summary; the returned model name is the one that produced the summary.
func (m *AIAgentModel) synthesizeSessionSummary(ctx context.Context, roomId string, summary *plugnmeet.AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry, promptTokens, completionTokens *uint32, log *logrus.Entry) string {
	if summary == nil {
		return ""
	}

	account, serviceCfg, _, err := m.resolveAIAgentProvider(ctx, roomId)
	if err != nil || account == nil {
		log.WithError(err).Debug("no provider account for the ai agent summary")
		return ""
	}
	summarizeModel := m.aiAgentSummarizeModel(serviceCfg)
	if summarizeModel == "" {
		return ""
	}
	provider, err := insightsservice.NewProvider(&insightsservice.ProviderArgs{
		Ctx:             ctx,
		ProviderType:    serviceCfg.Provider,
		ProviderAccount: account,
		ServiceConfig:   serviceCfg,
		Logger:          m.logger,
		RDS:             m.rds,
	})
	if err != nil || provider == nil {
		log.WithError(err).Debug("could not create the provider for the ai agent summary")
		return ""
	}

	p, c, err := applyLLMSummarySynthesis(ctx, provider, summarizeModel, summary, telemetry)
	*promptTokens, *completionTokens = p, c
	if err != nil {
		log.WithError(err).Debug("the llm summary synthesis failed, keeping the local summary")
	}
	return summarizeModel
}

// sortedProgressEntries orders the progress hash by concept for a stable artifact.
func sortedProgressEntries(progressMap map[string]redisservice.AIProgressEntry) []redisservice.AIProgressEntry {
	entries := make([]redisservice.AIProgressEntry, 0, len(progressMap))
	for _, entry := range progressMap {
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Concept) < strings.ToLower(entries[j].Concept)
	})
	return entries
}

// collectQuizEvaluation aggregates the room's quiz polls: how many answers were
// correct and whether the group passed each question.
func collectQuizEvaluation(ctx context.Context, rs *redisservice.RedisService, roomID string) *plugnmeet.AIAgentSummaryQuizEvaluation {
	evaluation := &plugnmeet.AIAgentSummaryQuizEvaluation{Questions: []*plugnmeet.AIAgentSummaryQuizQuestion{}}
	if rs == nil {
		return evaluation
	}

	polls, err := rs.GetRoomPolls(roomID)
	if err != nil {
		return evaluation
	}
	// The poll index is a Redis SET, so order it to keep the artifact stable.
	sort.SliceStable(polls, func(i, j int) bool {
		if polls[i].GetCreated() != polls[j].GetCreated() {
			return polls[i].GetCreated() < polls[j].GetCreated()
		}
		return polls[i].GetId() < polls[j].GetId()
	})

	for _, info := range polls {
		if info == nil {
			continue
		}

		correctIndex, found := -1, false
		if idx, _, ok, err := rs.GetAIQuizMetadata(ctx, roomID, info.GetId()); err == nil && ok {
			correctIndex, found = idx, true
		}
		if !found {
			if !info.GetIsQuiz() {
				continue
			}
			for i, option := range info.GetOptions() {
				if option.GetIsCorrect() {
					correctIndex = i
					break
				}
			}
		}
		if correctIndex < 0 || correctIndex >= len(info.GetOptions()) {
			continue
		}

		details, err := rs.GetPollResponsesDetails(roomID, info.GetId(), info)
		if err != nil {
			continue
		}

		correctOption := info.GetOptions()[correctIndex]
		question := &plugnmeet.AIAgentSummaryQuizQuestion{
			PollId:        info.GetId(),
			Question:      info.GetQuestion(),
			CorrectOption: correctOption.GetText(),
		}
		for _, option := range details.GetOptions() {
			question.TotalVotes += option.GetVoteCount()
			if option.GetId() == uint64(correctOption.GetId()) {
				question.CorrectVotes = option.GetVoteCount()
			}
		}
		question.Passed = question.CorrectVotes > 0 && question.CorrectVotes*2 >= question.TotalVotes

		evaluation.TotalQuizzes++
		evaluation.TotalResponses += int32(question.TotalVotes)
		evaluation.CorrectResponses += int32(question.CorrectVotes)
		evaluation.Questions = append(evaluation.Questions, question)
	}

	if evaluation.TotalResponses > 0 {
		evaluation.AccuracyPercent = int32(math.Round(float64(evaluation.CorrectResponses) * 100 / float64(evaluation.TotalResponses)))
	}
	return evaluation
}

// buildDeterministicSessionSummary builds the local_fallback summary: everything
// it says is derived from telemetry, progress entries and quiz counters.
func buildDeterministicSessionSummary(roomID, roomSid string, telemetry *redisservice.AIAgentSessionTelemetry, progress []redisservice.AIProgressEntry, quizzes *plugnmeet.AIAgentSummaryQuizEvaluation) *plugnmeet.AIAgentSessionSummary {
	if telemetry == nil {
		telemetry = &redisservice.AIAgentSessionTelemetry{}
	}
	if quizzes == nil {
		quizzes = &plugnmeet.AIAgentSummaryQuizEvaluation{}
	}

	endedAt := telemetry.EndedAt
	if endedAt == 0 {
		endedAt = time.Now().UnixMilli()
	}
	var duration int64
	if telemetry.StartedAt > 0 && endedAt > telemetry.StartedAt {
		duration = (endedAt - telemetry.StartedAt) / 1000
	}

	summary := &plugnmeet.AIAgentSessionSummary{
		SummarySource: AIAgentSummarySourceLocalFallback,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		// The learner is always recorded, even while still unknown: the finalize
		// step merges the participant it resolves over these values.
		UserInfo: &plugnmeet.RoomArtifactUserInfo{
			UserId:    telemetry.UserID,
			Name:      optionalString(telemetry.UserName),
			ExUserId:  optionalString(telemetry.ExUserID),
			ExtraData: optionalString(telemetry.ExtraData),
		},
		Session: &plugnmeet.AIAgentSummarySession{
			RoomId:                 roomID,
			RoomSid:                roomSid,
			SessionId:              telemetry.SessionID,
			AgentDisplayName:       telemetry.AgentName,
			Topic:                  telemetry.Topic,
			AllowedSubtopics:       append([]string(nil), telemetry.AllowedSubtopics...),
			StartedAt:              telemetry.StartedAt,
			EndedAt:                endedAt,
			DurationSeconds:        duration,
			EndReason:              telemetry.EndReason,
			OffTopicRedirectsCount: int32(telemetry.OffTopicRedirectsCount),
			TurnsCount:             int32(telemetry.TurnsCount),
		},
		QuizEvaluation:    quizzes,
		ProgressLog:       buildProgressLog(progress),
		WorkspaceActivity: buildWorkspaceActivity(telemetry.ToolsUsed),
	}
	if summary.QuizEvaluation.Questions == nil {
		summary.QuizEvaluation.Questions = []*plugnmeet.AIAgentSummaryQuizQuestion{}
	}

	summary.Evaluation, summary.Continuation = buildDeterministicEvaluation(telemetry, progress, quizzes)
	summary.NarrativeSummary = buildDeterministicNarrative(summary)
	return summary
}

// buildProgressLog converts the stored progress entries into the artifact shape.
func buildProgressLog(progress []redisservice.AIProgressEntry) []*plugnmeet.AIAgentSummaryProgressEntry {
	entries := make([]*plugnmeet.AIAgentSummaryProgressEntry, 0, len(progress))
	for _, entry := range progress {
		entries = append(entries, &plugnmeet.AIAgentSummaryProgressEntry{
			Concept: entry.Concept,
			Status:  entry.Status,
			Notes:   optionalString(entry.Notes),
		})
	}
	return entries
}

func buildWorkspaceActivity(toolsUsed map[string]int) *plugnmeet.AIAgentSummaryWorkspaceActivity {
	activity := &plugnmeet.AIAgentSummaryWorkspaceActivity{ToolsUsed: make(map[string]int32, len(toolsUsed))}
	for name, count := range toolsUsed {
		activity.ToolsUsed[name] = int32(count)
		if count <= 0 {
			continue
		}
		switch {
		case strings.HasPrefix(name, "whiteboard_"):
			activity.WhiteboardUsed = true
		case strings.HasPrefix(name, "notepad_"):
			activity.NotepadUsed = true
		}
	}
	return activity
}

func buildDeterministicEvaluation(telemetry *redisservice.AIAgentSessionTelemetry, progress []redisservice.AIProgressEntry, quizzes *plugnmeet.AIAgentSummaryQuizEvaluation) (*plugnmeet.AIAgentSummaryEvaluation, *plugnmeet.AIAgentSummaryContinuation) {
	if quizzes == nil {
		quizzes = &plugnmeet.AIAgentSummaryQuizEvaluation{}
	}
	evaluation := &plugnmeet.AIAgentSummaryEvaluation{
		ConceptsMastered:     []string{},
		ConceptsPracticed:    []string{},
		StruggledWith:        []string{},
		VocabularyOrKeyTerms: []string{},
	}

	seen := make(map[string]struct{}, len(progress))
	for _, entry := range progress {
		concept := strings.TrimSpace(entry.Concept)
		if concept == "" {
			continue
		}
		if _, ok := seen[strings.ToLower(concept)]; ok {
			continue
		}
		seen[strings.ToLower(concept)] = struct{}{}

		switch strings.ToLower(strings.TrimSpace(entry.Status)) {
		case "mastered":
			evaluation.ConceptsMastered = append(evaluation.ConceptsMastered, concept)
		case "needs_review", "struggled":
			evaluation.StruggledWith = append(evaluation.StruggledWith, concept)
		case "practiced", "introduced":
			evaluation.ConceptsPracticed = append(evaluation.ConceptsPracticed, concept)
		}
	}

	switch {
	case quizzes.TotalQuizzes > 0:
		evaluation.OverallScore = quizzes.AccuracyPercent
	case len(seen) > 0:
		evaluation.OverallScore = int32(len(evaluation.ConceptsMastered) * 100 / len(seen))
	}
	evaluation.MasteryLevel = masteryLevelForScore(int(evaluation.OverallScore))
	evaluation.ParticipationLevel = participationLevelForTurns(telemetry.TurnsCount)

	if len(seen) > 0 || quizzes.TotalResponses > 0 {
		evaluation.RubricNotes = fmt.Sprintf("Derived locally: %d of %d concepts mastered, %d of %d quiz answers correct (%d%%).",
			len(evaluation.ConceptsMastered), len(seen), quizzes.CorrectResponses, quizzes.TotalResponses, quizzes.AccuracyPercent)
	}

	continuation := &plugnmeet.AIAgentSummaryContinuation{
		UnfinishedSubtopics: unfinishedSubtopics(telemetry.AllowedSubtopics, evaluation.ConceptsMastered),
	}
	switch {
	case len(continuation.UnfinishedSubtopics) > 0:
		continuation.NextSessionFocus = fmt.Sprintf("Finish %q and the remaining subtopics.", continuation.UnfinishedSubtopics[0])
	case len(evaluation.StruggledWith) > 0:
		continuation.NextSessionFocus = fmt.Sprintf("Revisit %q before moving on.", evaluation.StruggledWith[0])
	case strings.TrimSpace(telemetry.Topic) != "":
		continuation.NextSessionFocus = fmt.Sprintf("Extend %q with new material.", telemetry.Topic)
	}
	switch {
	case len(evaluation.StruggledWith) > 0:
		continuation.RecommendedWarmUp = fmt.Sprintf("Two-minute recap of %q, then one quick quiz question.", evaluation.StruggledWith[0])
	case len(evaluation.ConceptsMastered) > 0:
		continuation.RecommendedWarmUp = fmt.Sprintf("Quick recall of %q to keep it fresh.", evaluation.ConceptsMastered[0])
	case strings.TrimSpace(telemetry.Topic) != "":
		continuation.RecommendedWarmUp = fmt.Sprintf("Two-minute recap of %q.", telemetry.Topic)
	}
	continuation.PreviousSessionContextPrompt = buildPreviousSessionContextPrompt(telemetry.Topic, evaluation, continuation.NextSessionFocus)

	return evaluation, continuation
}

func masteryLevelForScore(score int) string {
	switch {
	case score >= 85:
		return "advanced"
	case score >= 70:
		return "proficient"
	case score >= 50:
		return "developing"
	default:
		return "beginner"
	}
}

func participationLevelForTurns(turns int) string {
	switch {
	case turns >= 10:
		return "high"
	case turns >= 3:
		return "medium"
	default:
		return "low"
	}
}

// unfinishedSubtopics returns the allowed subtopics that were not mastered yet.
func unfinishedSubtopics(allowed, mastered []string) []string {
	done := make(map[string]struct{}, len(mastered))
	for _, concept := range mastered {
		done[strings.ToLower(strings.TrimSpace(concept))] = struct{}{}
	}

	unfinished := []string{}
	for _, subtopic := range allowed {
		subtopic = strings.TrimSpace(subtopic)
		if subtopic == "" {
			continue
		}
		if _, ok := done[strings.ToLower(subtopic)]; ok {
			continue
		}
		unfinished = append(unfinished, subtopic)
	}
	return unfinished
}

// buildPreviousSessionContextPrompt renders the short blob a host can feed back
// as previous_session_context on the next room.
func buildPreviousSessionContextPrompt(topic string, evaluation *plugnmeet.AIAgentSummaryEvaluation, focus string) string {
	parts := make([]string, 0, 5)
	if topic = strings.TrimSpace(topic); topic != "" {
		parts = append(parts, fmt.Sprintf("the topic was %q", topic))
	}
	if len(evaluation.ConceptsMastered) > 0 {
		parts = append(parts, "already mastered "+strings.Join(evaluation.ConceptsMastered, ", "))
	}
	if len(evaluation.ConceptsPracticed) > 0 {
		parts = append(parts, "practised "+strings.Join(evaluation.ConceptsPracticed, ", "))
	}
	if len(evaluation.StruggledWith) > 0 {
		parts = append(parts, "struggled with "+strings.Join(evaluation.StruggledWith, ", "))
	}
	if focus = strings.TrimSpace(focus); focus != "" {
		parts = append(parts, "next focus: "+focus)
	}
	if len(parts) == 0 {
		return ""
	}
	return truncate("In the previous session "+strings.Join(parts, "; ")+".", aiAgentSummaryMaxContextRunes)
}

func buildDeterministicNarrative(summary *plugnmeet.AIAgentSessionSummary) string {
	name := firstNonEmptyString(summary.Session.AgentDisplayName, aiAgentDefaultDisplayName)
	topic := strings.TrimSpace(summary.Session.Topic)
	subject := fmt.Sprintf("%s ran a session", name)
	if topic != "" {
		subject = fmt.Sprintf("%s ran a session on %q", name, topic)
	}

	narrative := fmt.Sprintf("%s that ended after %d minute(s) and %d participant turn(s), with %d off-topic redirect(s).",
		subject, summary.Session.DurationSeconds/60, summary.Session.TurnsCount, summary.Session.OffTopicRedirectsCount)
	if summary.QuizEvaluation.TotalQuizzes > 0 {
		narrative += fmt.Sprintf(" Quizzes: %d of %d answers correct (%d%% accuracy).",
			summary.QuizEvaluation.CorrectResponses, summary.QuizEvaluation.TotalResponses, summary.QuizEvaluation.AccuracyPercent)
	}
	if len(summary.Evaluation.ConceptsMastered) > 0 {
		narrative += " Mastered: " + strings.Join(summary.Evaluation.ConceptsMastered, ", ") + "."
	}
	if len(summary.Evaluation.StruggledWith) > 0 {
		narrative += " Needs review: " + strings.Join(summary.Evaluation.StruggledWith, ", ") + "."
	}
	if focus := strings.TrimSpace(summary.Continuation.NextSessionFocus); focus != "" {
		narrative += " Next step: " + focus
	}
	return narrative
}

// mergeAIAgentSummaryOverlay copies only the non-empty fields of the model's
// answer over the deterministic summary.
func mergeAIAgentSummaryOverlay(summary, o *plugnmeet.AIAgentSessionSummary) {
	if o == nil || summary == nil {
		return
	}

	if eval := o.Evaluation; eval != nil {
		if summary.Evaluation == nil {
			summary.Evaluation = &plugnmeet.AIAgentSummaryEvaluation{}
		}
		if eval.OverallScore > 0 {
			summary.Evaluation.OverallScore = eval.OverallScore
			summary.Evaluation.MasteryLevel = masteryLevelForScore(int(eval.OverallScore))
		}
		if level := normalizeMasteryLevel(eval.MasteryLevel); level != "" {
			summary.Evaluation.MasteryLevel = level
		}
		if level := normalizeParticipationLevel(eval.ParticipationLevel); level != "" {
			summary.Evaluation.ParticipationLevel = level
		}
		if len(eval.ConceptsMastered) > 0 {
			summary.Evaluation.ConceptsMastered = eval.ConceptsMastered
		}
		if len(eval.ConceptsPracticed) > 0 {
			summary.Evaluation.ConceptsPracticed = eval.ConceptsPracticed
		}
		if len(eval.StruggledWith) > 0 {
			summary.Evaluation.StruggledWith = eval.StruggledWith
		}
		if len(eval.VocabularyOrKeyTerms) > 0 {
			summary.Evaluation.VocabularyOrKeyTerms = eval.VocabularyOrKeyTerms
		}
		if notes := strings.TrimSpace(eval.RubricNotes); notes != "" {
			summary.Evaluation.RubricNotes = notes
		}
	}

	if continuation := o.Continuation; continuation != nil {
		if summary.Continuation == nil {
			summary.Continuation = &plugnmeet.AIAgentSummaryContinuation{}
		}
		if focus := strings.TrimSpace(continuation.NextSessionFocus); focus != "" {
			summary.Continuation.NextSessionFocus = focus
		}
		if warmUp := strings.TrimSpace(continuation.RecommendedWarmUp); warmUp != "" {
			summary.Continuation.RecommendedWarmUp = warmUp
		}
		if len(continuation.UnfinishedSubtopics) > 0 {
			summary.Continuation.UnfinishedSubtopics = continuation.UnfinishedSubtopics
		}
		if prompt := strings.TrimSpace(continuation.PreviousSessionContextPrompt); prompt != "" {
			summary.Continuation.PreviousSessionContextPrompt =
				truncate(prompt, aiAgentSummaryMaxContextRunes)
		}
	}

	if narrative := strings.TrimSpace(o.NarrativeSummary); narrative != "" {
		summary.NarrativeSummary = narrative
	}
}

func normalizeMasteryLevel(level string) string {
	switch level = strings.ToLower(strings.TrimSpace(level)); level {
	case "advanced", "proficient", "developing", "beginner":
		return level
	}
	return ""
}

func normalizeParticipationLevel(level string) string {
	switch level = strings.ToLower(strings.TrimSpace(level)); level {
	case "high", "medium", "low":
		return level
	}
	return ""
}

// aiAgentSummarizeModel returns the text model that refines the session summary.
// The model comes strictly from the room's resolved ai_agent service, so a room
// with its own provider never borrows a model from another service.
func (m *AIAgentModel) aiAgentSummarizeModel(serviceCfg *config.ServiceConfig) string {
	if serviceCfg != nil {
		return strings.TrimSpace(serviceCfg.GetOptionsString("summarize_model", ""))
	}
	if m.appConfig == nil {
		return ""
	}

	return resolveAIAgentSummarizeModel(m.appConfig.Insights)
}

// resolveAIAgentSummarizeModel returns the summarize_model of the server-configured
// ai_agent service. An empty result means "no LLM synthesis, keep the local
// fallback"; it is never borrowed from ai_text_chat or meeting_summarizing.
func resolveAIAgentSummarizeModel(insightsCfg *config.InsightsConfig) string {
	if insightsCfg == nil {
		return ""
	}
	agentService, ok := insightsCfg.Services[insights.ServiceTypeAIAgent]
	if !ok || agentService == nil {
		return ""
	}

	return strings.TrimSpace(agentService.GetOptionsString("summarize_model", ""))
}

// applyLLMSummarySynthesis lets the model refine the deterministic summary. Any
// failure keeps the fallback untouched, so the artifact is always written.
func applyLLMSummarySynthesis(ctx context.Context, provider insights.Provider, summarizeModel string, summary *plugnmeet.AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry) (promptTokens, completionTokens uint32, err error) {
	if provider == nil || summary == nil {
		return 0, 0, fmt.Errorf("ai agent summary: missing provider or summary")
	}

	sumCtx, cancel := context.WithTimeout(ctx, aiAgentSummaryBudget)
	defer cancel()

	text, promptTokens, completionTokens, err := provider.AIChatTextSummarize(sumCtx, summarizeModel, buildAIAgentSummaryPrompt(summary, telemetry))
	if err != nil {
		return promptTokens, completionTokens, err
	}

	overlay := new(plugnmeet.AIAgentSessionSummary)
	unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshal.Unmarshal([]byte(stripJSONFence(text)), overlay); err != nil {
		return promptTokens, completionTokens, fmt.Errorf("ai agent summary: invalid model JSON: %w", err)
	}

	mergeAIAgentSummaryOverlay(summary, overlay)
	summary.SummarySource = AIAgentSummarySourceLLM
	return promptTokens, completionTokens, nil
}

func buildAIAgentSummaryPrompt(summary *plugnmeet.AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry) []*plugnmeet.InsightsAITextChatContent {
	systemPrompt := `You finalize an AI agent session report for the room host.
Use only the deterministic summary and the transcript; never invent facts.
Reply with one JSON object and nothing else, using exactly this shape:
{"evaluation":{"overall_score":0,"mastery_level":"","participation_level":"","concepts_mastered":[],"concepts_practiced":[],"struggled_with":[],"vocabulary_or_key_terms":[],"rubric_notes":""},"continuation":{"next_session_focus":"","recommended_warm_up":"","unfinished_subtopics":[],"previous_session_context_prompt":""},"narrative_summary":""}
overall_score is 0-100, mastery_level is advanced|proficient|developing|beginner,
participation_level is high|medium|low. Omit any field you cannot improve.`

	deterministic, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(summary)
	if err != nil {
		deterministic = []byte("{}")
	}

	var transcript []redisservice.AISessionTranscriptTurn
	var rollingSummary string
	if telemetry != nil {
		transcript = telemetry.Transcript
		rollingSummary = strings.TrimSpace(telemetry.RollingSummary)
	}

	ctx := "Session transcript:\n" + formatSessionTranscript(transcript)
	if rollingSummary != "" {
		// The transcript ring buffer drops the oldest turns, so the running summary
		// is what still knows about the first part of a long session.
		ctx = "Running session summary:\n" + rollingSummary + "\n\n" + ctx
	}

	return []*plugnmeet.InsightsAITextChatContent{
		{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
			Text: systemPrompt,
		},
		{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
			Text: "Deterministic summary:\n" + string(deterministic) + "\n\n" + ctx,
		},
	}
}

// formatSessionTranscript keeps the newest turns and trims each line so a long
// session still fits into one request.
func formatSessionTranscript(transcript []redisservice.AISessionTranscriptTurn) string {
	turns := transcript
	if len(turns) > aiAgentSummaryMaxTranscriptTurns {
		turns = turns[len(turns)-aiAgentSummaryMaxTranscriptTurns:]
	}

	var b strings.Builder
	for _, turn := range turns {
		text := strings.TrimSpace(turn.Text)
		if text == "" {
			continue
		}
		role := firstNonEmptyString(turn.Role, "participant")
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(truncate(text, aiAgentSummaryMaxTranscriptRunes))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "(empty)"
	}
	return b.String()
}

// stripJSONFence removes an optional markdown fence around the model's JSON.
func stripJSONFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	text = strings.TrimPrefix(text, "```")
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[idx+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
}
