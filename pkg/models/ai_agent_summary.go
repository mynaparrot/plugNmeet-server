package models

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	insightsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

const (
	// AIAgentSummarySchemaVersion is the contract version the host depends on.
	AIAgentSummarySchemaVersion = 1

	AIAgentSummarySourceLLM           = "llm"
	AIAgentSummarySourceLocalFallback = "local_fallback"

	// aiAgentSummaryBudget is the hard cap of the LLM synthesis attempt: a
	// missing artifact is worse than a slightly worse one.
	aiAgentSummaryBudget = 5 * time.Second

	aiAgentSummaryMaxTranscriptTurns = 80
	aiAgentSummaryMaxTranscriptRunes = 400
	aiAgentSummaryMaxContextRunes    = 1200
)

// AIAgentSessionSummary is the versioned JSON contract written to the
// AI_AGENT_SESSION_SUMMARY artifact.
type AIAgentSessionSummary struct {
	SchemaVersion     int                             `json:"schema_version"`
	SummarySource     string                          `json:"summary_source"`
	GeneratedAt       string                          `json:"generated_at"`
	Session           AIAgentSummarySession           `json:"session"`
	Evaluation        AIAgentSummaryEvaluation        `json:"evaluation"`
	QuizEvaluation    AIAgentSummaryQuizEvaluation    `json:"quiz_evaluation"`
	ProgressLog       []redisservice.AIProgressEntry  `json:"progress_log"`
	WorkspaceActivity AIAgentSummaryWorkspaceActivity `json:"workspace_activity"`
	Continuation      AIAgentSummaryContinuation      `json:"continuation"`
	NarrativeSummary  string                          `json:"narrative_summary"`
}

type AIAgentSummarySession struct {
	RoomID                 string   `json:"room_id"`
	RoomSid                string   `json:"room_sid"`
	SessionID              string   `json:"session_id"`
	AgentDisplayName       string   `json:"agent_display_name"`
	Topic                  string   `json:"topic"`
	AllowedSubtopics       []string `json:"allowed_subtopics"`
	StartedAt              int64    `json:"started_at"`
	EndedAt                int64    `json:"ended_at"`
	DurationSeconds        int64    `json:"duration_seconds"`
	EndReason              string   `json:"end_reason"`
	OffTopicRedirectsCount int      `json:"off_topic_redirects_count"`
	TurnsCount             int      `json:"turns_count"`
}

type AIAgentSummaryEvaluation struct {
	OverallScore         int      `json:"overall_score"`
	MasteryLevel         string   `json:"mastery_level"`
	ParticipationLevel   string   `json:"participation_level"`
	ConceptsMastered     []string `json:"concepts_mastered"`
	ConceptsPracticed    []string `json:"concepts_practiced"`
	StruggledWith        []string `json:"struggled_with"`
	VocabularyOrKeyTerms []string `json:"vocabulary_or_key_terms"`
	RubricNotes          string   `json:"rubric_notes"`
}

type AIAgentSummaryQuizEvaluation struct {
	TotalQuizzes     int                          `json:"total_quizzes"`
	TotalResponses   int                          `json:"total_responses"`
	CorrectResponses int                          `json:"correct_responses"`
	AccuracyPercent  int                          `json:"accuracy_percent"`
	Questions        []AIAgentSummaryQuizQuestion `json:"questions"`
}

type AIAgentSummaryQuizQuestion struct {
	PollID        string `json:"poll_id"`
	Question      string `json:"question"`
	CorrectOption string `json:"correct_option"`
	TotalVotes    uint64 `json:"total_votes"`
	CorrectVotes  uint64 `json:"correct_votes"`
	Passed        bool   `json:"passed"`
}

type AIAgentSummaryWorkspaceActivity struct {
	ToolsUsed      map[string]int `json:"tools_used"`
	WhiteboardUsed bool           `json:"whiteboard_used"`
	NotepadUsed    bool           `json:"notepad_used"`
}

type AIAgentSummaryContinuation struct {
	NextSessionFocus             string   `json:"next_session_focus"`
	RecommendedWarmUp            string   `json:"recommended_warm_up"`
	UnfinishedSubtopics          []string `json:"unfinished_subtopics"`
	PreviousSessionContextPrompt string   `json:"previous_session_context_prompt"`
}

// FinalizeSession stops the room's agent, writes the session summary artifact
// and drops the Redis state the summary was built from. A room without any
// agent activity returns nil.
func (m *AIAgentModel) FinalizeSession(ctx context.Context, roomTableId uint64, roomId, roomSid string) *AIAgentSessionSummary {
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

	var promptTokens, completionTokens uint32
	summarizeModel := m.synthesizeSessionSummary(cleanupCtx, summary, telemetry, &promptTokens, &completionTokens, log)

	if m.artifactModel != nil && roomTableId > 0 {
		if _, err := m.artifactModel.CreateAIAgentSessionSummaryArtifact(roomTableId, roomId, roomSid, summary, telemetry, promptTokens, completionTokens, summarizeModel, m.logger); err != nil {
			log.WithError(err).Error("failed to create the ai agent session summary artifact")
		}
	}

	m.redisService.DeleteAIAgentSessionData(cleanupCtx, roomId)
	m.cleanupRoomState(cleanupCtx, roomId)
	return summary
}

// synthesizeSessionSummary lets the configured model refine the deterministic
// summary; the returned model name is the one that produced the summary.
func (m *AIAgentModel) synthesizeSessionSummary(ctx context.Context, summary *AIAgentSessionSummary, telemetry *redisservice.AIAgentSessionTelemetry, promptTokens, completionTokens *uint32, log *logrus.Entry) string {
	if summary == nil || m.appConfig == nil || m.appConfig.Insights == nil {
		return ""
	}
	summarizeModel := resolveAIAgentSummarizeModel(m.appConfig.Insights)
	if summarizeModel == "" {
		return ""
	}

	account, serviceCfg, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)
	if err != nil || account == nil {
		log.WithError(err).Debug("no provider account for the ai agent summary")
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

	var transcript []redisservice.AISessionTranscriptTurn
	if telemetry != nil {
		transcript = telemetry.Transcript
	}
	p, c, err := applyLLMSummarySynthesis(ctx, provider, summarizeModel, summary, transcript)
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
func collectQuizEvaluation(ctx context.Context, rs *redisservice.RedisService, roomID string) AIAgentSummaryQuizEvaluation {
	evaluation := AIAgentSummaryQuizEvaluation{Questions: []AIAgentSummaryQuizQuestion{}}
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
		question := AIAgentSummaryQuizQuestion{
			PollID:        info.GetId(),
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
		evaluation.TotalResponses += int(question.TotalVotes)
		evaluation.CorrectResponses += int(question.CorrectVotes)
		evaluation.Questions = append(evaluation.Questions, question)
	}

	if evaluation.TotalResponses > 0 {
		evaluation.AccuracyPercent = int(math.Round(float64(evaluation.CorrectResponses) * 100 / float64(evaluation.TotalResponses)))
	}
	return evaluation
}

// buildDeterministicSessionSummary builds the local_fallback summary: everything
// it says is derived from telemetry, progress entries and quiz counters.
func buildDeterministicSessionSummary(roomID, roomSid string, telemetry *redisservice.AIAgentSessionTelemetry, progress []redisservice.AIProgressEntry, quizzes AIAgentSummaryQuizEvaluation) *AIAgentSessionSummary {
	if telemetry == nil {
		telemetry = &redisservice.AIAgentSessionTelemetry{}
	}

	endedAt := telemetry.EndedAt
	if endedAt == 0 {
		endedAt = time.Now().UnixMilli()
	}
	var duration int64
	if telemetry.StartedAt > 0 && endedAt > telemetry.StartedAt {
		duration = (endedAt - telemetry.StartedAt) / 1000
	}

	summary := &AIAgentSessionSummary{
		SchemaVersion: AIAgentSummarySchemaVersion,
		SummarySource: AIAgentSummarySourceLocalFallback,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Session: AIAgentSummarySession{
			RoomID:                 roomID,
			RoomSid:                roomSid,
			SessionID:              telemetry.SessionID,
			AgentDisplayName:       telemetry.AgentName,
			Topic:                  telemetry.Topic,
			AllowedSubtopics:       append([]string(nil), telemetry.AllowedSubtopics...),
			StartedAt:              telemetry.StartedAt,
			EndedAt:                endedAt,
			DurationSeconds:        duration,
			EndReason:              telemetry.EndReason,
			OffTopicRedirectsCount: telemetry.OffTopicRedirectsCount,
			TurnsCount:             telemetry.TurnsCount,
		},
		QuizEvaluation:    quizzes,
		ProgressLog:       append([]redisservice.AIProgressEntry{}, progress...),
		WorkspaceActivity: buildWorkspaceActivity(telemetry.ToolsUsed),
	}
	if summary.QuizEvaluation.Questions == nil {
		summary.QuizEvaluation.Questions = []AIAgentSummaryQuizQuestion{}
	}

	summary.Evaluation, summary.Continuation = buildDeterministicEvaluation(telemetry, progress, quizzes)
	summary.NarrativeSummary = buildDeterministicNarrative(summary)
	return summary
}

func buildWorkspaceActivity(toolsUsed map[string]int) AIAgentSummaryWorkspaceActivity {
	activity := AIAgentSummaryWorkspaceActivity{ToolsUsed: make(map[string]int, len(toolsUsed))}
	for name, count := range toolsUsed {
		activity.ToolsUsed[name] = count
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

func buildDeterministicEvaluation(telemetry *redisservice.AIAgentSessionTelemetry, progress []redisservice.AIProgressEntry, quizzes AIAgentSummaryQuizEvaluation) (AIAgentSummaryEvaluation, AIAgentSummaryContinuation) {
	evaluation := AIAgentSummaryEvaluation{
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
		evaluation.OverallScore = len(evaluation.ConceptsMastered) * 100 / len(seen)
	}
	evaluation.MasteryLevel = masteryLevelForScore(evaluation.OverallScore)
	evaluation.ParticipationLevel = participationLevelForTurns(telemetry.TurnsCount)

	if len(seen) > 0 || quizzes.TotalResponses > 0 {
		evaluation.RubricNotes = fmt.Sprintf("Derived locally: %d of %d concepts mastered, %d of %d quiz answers correct (%d%%).",
			len(evaluation.ConceptsMastered), len(seen), quizzes.CorrectResponses, quizzes.TotalResponses, quizzes.AccuracyPercent)
	}

	continuation := AIAgentSummaryContinuation{
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
func buildPreviousSessionContextPrompt(topic string, evaluation AIAgentSummaryEvaluation, focus string) string {
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

func buildDeterministicNarrative(summary *AIAgentSessionSummary) string {
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

// aiAgentSummaryOverlay is what the model is allowed to improve on top of the
// deterministic summary.
type aiAgentSummaryOverlay struct {
	Evaluation       *AIAgentSummaryEvaluation   `json:"evaluation"`
	Continuation     *AIAgentSummaryContinuation `json:"continuation"`
	NarrativeSummary string                      `json:"narrative_summary"`
}

// merge copies only the non-empty overlay fields over the deterministic summary.
func (o *aiAgentSummaryOverlay) merge(summary *AIAgentSessionSummary) {
	if o == nil || summary == nil {
		return
	}

	if eval := o.Evaluation; eval != nil {
		if eval.OverallScore > 0 {
			summary.Evaluation.OverallScore = eval.OverallScore
			summary.Evaluation.MasteryLevel = masteryLevelForScore(eval.OverallScore)
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

// resolveAIAgentSummarizeModel returns the text model used to refine the session
// summary. An empty result means "no LLM synthesis, keep the local fallback".
func resolveAIAgentSummarizeModel(insightsCfg *config.InsightsConfig) string {
	if insightsCfg == nil {
		return ""
	}
	agentService, ok := insightsCfg.Services[insights.ServiceTypeAIAgent]
	if !ok || agentService == nil {
		return ""
	}
	if model := strings.TrimSpace(agentService.GetOptionsString("summarize_model", "")); model != "" {
		return model
	}
	// Fall back to another text service of the same provider: the credentials
	// are already known to serve text chat.
	for _, serviceType := range []insights.ServiceType{
		insights.ServiceTypeAITextChat,
		insights.ServiceTypeMeetingSummarizing,
	} {
		service, ok := insightsCfg.Services[serviceType]
		if !ok || service == nil || service.Provider != agentService.Provider {
			continue
		}
		if model := strings.TrimSpace(service.GetOptionsString("summarize_model", "")); model != "" {
			return model
		}
	}
	return ""
}

// applyLLMSummarySynthesis lets the model refine the deterministic summary. Any
// failure keeps the fallback untouched, so the artifact is always written.
func applyLLMSummarySynthesis(ctx context.Context, provider insights.Provider, summarizeModel string, summary *AIAgentSessionSummary, transcript []redisservice.AISessionTranscriptTurn) (promptTokens, completionTokens uint32, err error) {
	if provider == nil || summary == nil {
		return 0, 0, fmt.Errorf("ai agent summary: missing provider or summary")
	}

	sumCtx, cancel := context.WithTimeout(ctx, aiAgentSummaryBudget)
	defer cancel()

	text, promptTokens, completionTokens, err := provider.AIChatTextSummarize(sumCtx, summarizeModel, buildAIAgentSummaryPrompt(summary, transcript))
	if err != nil {
		return promptTokens, completionTokens, err
	}

	overlay := new(aiAgentSummaryOverlay)
	if err := json.Unmarshal([]byte(stripJSONFence(text)), overlay); err != nil {
		return promptTokens, completionTokens, fmt.Errorf("ai agent summary: invalid model JSON: %w", err)
	}

	overlay.merge(summary)
	summary.SummarySource = AIAgentSummarySourceLLM
	return promptTokens, completionTokens, nil
}

func buildAIAgentSummaryPrompt(summary *AIAgentSessionSummary, transcript []redisservice.AISessionTranscriptTurn) []*plugnmeet.InsightsAITextChatContent {
	systemPrompt := `You finalize an AI agent session report for the room host.
Use only the deterministic summary and the transcript; never invent facts.
Reply with one JSON object and nothing else, using exactly this shape:
{"evaluation":{"overall_score":0,"mastery_level":"","participation_level":"","concepts_mastered":[],"concepts_practiced":[],"struggled_with":[],"vocabulary_or_key_terms":[],"rubric_notes":""},"continuation":{"next_session_focus":"","recommended_warm_up":"","unfinished_subtopics":[],"previous_session_context_prompt":""},"narrative_summary":""}
overall_score is 0-100, mastery_level is advanced|proficient|developing|beginner,
participation_level is high|medium|low. Omit any field you cannot improve.`

	deterministic, err := json.Marshal(summary)
	if err != nil {
		deterministic = []byte("{}")
	}

	return []*plugnmeet.InsightsAITextChatContent{
		{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
			Text: systemPrompt,
		},
		{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
			Text: "Deterministic summary:\n" + string(deterministic) + "\n\nSession transcript:\n" + formatSessionTranscript(transcript),
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
