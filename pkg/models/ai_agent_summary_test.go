package models

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// fakeSummaryProvider answers AIChatTextSummarize from fixed values and records
// the call, so the synthesis path can be tested without a real model.
type fakeSummaryProvider struct {
	text    string
	err     error
	model   string
	history []*plugnmeet.InsightsAITextChatContent
}

func (f *fakeSummaryProvider) AIChatTextSummarize(_ context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	f.model = summarizeModel
	f.history = history
	if f.err != nil {
		return "", 7, 3, f.err
	}
	return f.text, 7, 3, nil
}

func (f *fakeSummaryProvider) CreateTranscription(context.Context, string, string, []byte) (insights.TranscriptionStream, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryProvider) TranslateText(context.Context, string, string, []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryProvider) SynthesizeText(context.Context, []byte) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryProvider) GetSupportedLanguages(insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	return nil
}

func (f *fakeSummaryProvider) AITextChatStream(context.Context, string, []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryProvider) StartBatchSummarizeAudioFile(context.Context, string, string, string) (string, string, error) {
	return "", "", errors.New("not implemented")
}

func (f *fakeSummaryProvider) CheckBatchJobStatus(context.Context, string) (*insights.BatchJobResponse, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeSummaryProvider) DeleteUploadedFile(context.Context, string) error {
	return errors.New("not implemented")
}

func (f *fakeSummaryProvider) CreateRealtimeSession(context.Context, string, *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	return nil, insights.ErrRealtimeNotSupported
}

// newSummaryQuizPoll registers one running quiz poll with the given options.
func newSummaryQuizPoll(t *testing.T, rs *redisservice.RedisService, rc redis.UniversalClient, roomID, pollID, question string, options ...*plugnmeet.CreatePollOptions) {
	t.Helper()
	info := &plugnmeet.PollInfo{
		Id:        pollID,
		RoomId:    roomID,
		Question:  question,
		Options:   options,
		IsRunning: true,
		Created:   time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	if err := rs.CreateRoomPoll(roomID, info); err != nil {
		t.Fatalf("could not create the poll %s: %v", pollID, err)
	}
	t.Cleanup(func() {
		_ = rc.FlushAll(context.Background()).Err()
	})
}

func TestCollectQuizEvaluation(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	m := newAIAgentTestModel(rc)
	ctx := context.Background()

	newSummaryQuizPoll(t, m.redisService, rc, "room-quiz", "poll-1", "2 + 2?",
		&plugnmeet.CreatePollOptions{Id: 1, Text: "3"},
		&plugnmeet.CreatePollOptions{Id: 2, Text: "4"},
	)
	// The AI knows the answer key of a quiz it asked itself.
	if err := m.redisService.SaveAIQuizMetadata(ctx, "room-quiz", "poll-1", 1, "two plus two"); err != nil {
		t.Fatal(err)
	}
	for _, vote := range []struct {
		user   string
		option uint64
	}{
		{"u1", 2}, {"u2", 2}, {"u3", 1},
	} {
		if err := m.redisService.AddPollResponse(&plugnmeet.SubmitPollResponseReq{
			RoomId: "room-quiz", PollId: "poll-1", UserId: vote.user, Name: vote.user,
			SelectedOptions: []uint64{vote.option},
		}); err != nil {
			t.Fatalf("vote of %s failed: %v", vote.user, err)
		}
	}

	// A plain, non-quiz poll must be ignored.
	newSummaryQuizPoll(t, m.redisService, rc, "room-quiz", "poll-2", "favourite colour?",
		&plugnmeet.CreatePollOptions{Id: 1, Text: "blue"},
	)

	evaluation := collectQuizEvaluation(ctx, m.redisService, "room-quiz")
	if evaluation.TotalQuizzes != 1 || len(evaluation.Questions) != 1 {
		t.Fatalf("evaluation = %+v, want only the quiz poll", evaluation)
	}
	question := evaluation.Questions[0]
	if question.PollID != "poll-1" || question.CorrectOption != "4" {
		t.Errorf("question = %+v, want poll-1 with the key answer 4", question)
	}
	if question.TotalVotes != 3 || question.CorrectVotes != 2 {
		t.Errorf("votes = %d/%d, want 2 of 3", question.CorrectVotes, question.TotalVotes)
	}
	if !question.Passed {
		t.Error("a majority of correct answers must pass the question")
	}
	if evaluation.TotalResponses != 3 || evaluation.CorrectResponses != 2 || evaluation.AccuracyPercent != 67 {
		t.Errorf("evaluation counters = %+v, want 2 of 3 (67%%)", evaluation)
	}

	if empty := collectQuizEvaluation(ctx, nil, "room-quiz"); len(empty.Questions) != 0 || empty.TotalQuizzes != 0 {
		t.Errorf("a missing service must yield an empty evaluation, got %+v", empty)
	}
}

func TestBuildDeterministicSessionSummary(t *testing.T) {
	telemetry := &redisservice.AIAgentSessionTelemetry{
		SessionID:              "session-1",
		AgentName:              "Ms. Ava",
		Topic:                  "Grade 4 Fractions",
		AllowedSubtopics:       []string{"equivalent fractions", "adding fractions"},
		StartedAt:              1700000000000,
		EndedAt:                1700000600000,
		EndReason:              "room_ended",
		TurnsCount:             12,
		OffTopicRedirectsCount: 2,
		ToolsUsed: map[string]int{
			"whiteboard_write_content": 2,
			"notepad_write":            1,
		},
	}
	progress := []redisservice.AIProgressEntry{
		{Concept: "equivalent fractions", Status: "mastered"},
		{Concept: "adding fractions", Status: "needs_review"},
		{Concept: "number line", Status: "practiced"},
	}
	quizzes := AIAgentSummaryQuizEvaluation{
		TotalQuizzes:     1,
		TotalResponses:   4,
		CorrectResponses: 3,
		AccuracyPercent:  75,
		Questions: []AIAgentSummaryQuizQuestion{
			{PollID: "poll-1", Question: "1/2 = 2/4?", Passed: true},
		},
	}

	summary := buildDeterministicSessionSummary("room-1", "sid-1", telemetry, progress, quizzes)

	if summary.SchemaVersion != AIAgentSummarySchemaVersion || summary.SummarySource != AIAgentSummarySourceLocalFallback {
		t.Errorf("summary = %+v, want schema %d with the local fallback source", summary, AIAgentSummarySchemaVersion)
	}
	if summary.Session.DurationSeconds != 600 || summary.Session.RoomSid != "sid-1" || summary.Session.EndReason != "room_ended" {
		t.Errorf("session = %+v, want 600s of room-1/sid-1", summary.Session)
	}
	if summary.Evaluation.OverallScore != 75 || summary.Evaluation.MasteryLevel != "proficient" {
		t.Errorf("evaluation = %+v, want the 75%% quiz accuracy as proficient", summary.Evaluation)
	}
	if summary.Evaluation.ParticipationLevel != "high" {
		t.Errorf("participation = %q, want high for 12 turns", summary.Evaluation.ParticipationLevel)
	}
	if got := strings.Join(summary.Evaluation.ConceptsMastered, ","); got != "equivalent fractions" {
		t.Errorf("mastered = %q", got)
	}
	if got := strings.Join(summary.Evaluation.ConceptsPracticed, ","); got != "number line" {
		t.Errorf("practiced = %q", got)
	}
	if got := strings.Join(summary.Evaluation.StruggledWith, ","); got != "adding fractions" {
		t.Errorf("struggled = %q", got)
	}
	if got := strings.Join(summary.Continuation.UnfinishedSubtopics, ","); got != "adding fractions" {
		t.Errorf("unfinished = %q, want only the unmastered subtopic", got)
	}
	if !summary.WorkspaceActivity.WhiteboardUsed || !summary.WorkspaceActivity.NotepadUsed {
		t.Errorf("workspace activity = %+v, want both surfaces used", summary.WorkspaceActivity)
	}
	if summary.WorkspaceActivity.ToolsUsed["whiteboard_write_content"] != 2 {
		t.Errorf("tools used = %+v, want the whiteboard counter", summary.WorkspaceActivity.ToolsUsed)
	}
	if !strings.Contains(summary.Continuation.PreviousSessionContextPrompt, "previous session") {
		t.Errorf("prompt = %q, want the handoff sentence", summary.Continuation.PreviousSessionContextPrompt)
	}
	if !strings.Contains(summary.Continuation.PreviousSessionContextPrompt, "adding fractions") {
		t.Errorf("prompt = %q, want the concept to revisit", summary.Continuation.PreviousSessionContextPrompt)
	}
	if !strings.Contains(summary.NarrativeSummary, "Ms. Ava") || !strings.Contains(summary.NarrativeSummary, "75%") {
		t.Errorf("narrative = %q, want the agent name and the quiz accuracy", summary.NarrativeSummary)
	}

	// An empty telemetry must still produce a usable summary.
	empty := buildDeterministicSessionSummary("room-2", "sid-2", nil, nil, AIAgentSummaryQuizEvaluation{})
	if empty == nil || empty.Session.RoomID != "room-2" {
		t.Fatalf("empty summary = %+v", empty)
	}
	if empty.Evaluation.MasteryLevel != "beginner" || empty.QuizEvaluation.Questions == nil {
		t.Errorf("empty summary = %+v, want a beginner level and an empty question list", empty.Evaluation)
	}
}

func TestApplyLLMSummarySynthesis(t *testing.T) {
	base := func() *AIAgentSessionSummary {
		return buildDeterministicSessionSummary("room-1", "sid-1",
			&redisservice.AIAgentSessionTelemetry{Topic: "Fractions", TurnsCount: 4},
			[]redisservice.AIProgressEntry{{Concept: "halves", Status: "practiced"}},
			AIAgentSummaryQuizEvaluation{})
	}
	transcript := []redisservice.AISessionTranscriptTurn{
		{Role: "participant", Text: "what is a half"},
		{Role: "agent", Text: "one of two equal parts"},
	}

	t.Run("valid JSON is merged", func(t *testing.T) {
		provider := &fakeSummaryProvider{text: "```json\n" + `{"evaluation":{"overall_score":90,"concepts_mastered":["halves"]},"continuation":{"next_session_focus":"quarters"},"narrative_summary":"Strong session."}` + "\n```"}
		summary := base()

		promptTokens, completionTokens, err := applyLLMSummarySynthesis(context.Background(), provider, "gpt-4o-mini", summary, transcript)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if promptTokens != 7 || completionTokens != 3 {
			t.Errorf("tokens = %d/%d, want 7/3 from the provider", promptTokens, completionTokens)
		}
		if summary.SummarySource != AIAgentSummarySourceLLM {
			t.Errorf("source = %q, want llm", summary.SummarySource)
		}
		if summary.Evaluation.OverallScore != 90 || summary.Evaluation.MasteryLevel != "advanced" {
			t.Errorf("evaluation = %+v, want the model score", summary.Evaluation)
		}
		if summary.Continuation.NextSessionFocus != "quarters" || summary.NarrativeSummary != "Strong session." {
			t.Errorf("continuation = %+v, narrative = %q", summary.Continuation, summary.NarrativeSummary)
		}
		if provider.model != "gpt-4o-mini" {
			t.Errorf("model = %q, want the configured one", provider.model)
		}
		if len(provider.history) == 0 || !strings.Contains(provider.history[len(provider.history)-1].GetText(), "Deterministic summary") {
			t.Errorf("history = %v, want the deterministic summary in the prompt", provider.history)
		}
	})

	t.Run("provider error keeps the fallback", func(t *testing.T) {
		summary := base()
		_, _, err := applyLLMSummarySynthesis(context.Background(), &fakeSummaryProvider{err: errors.New("boom")}, "gpt-4o-mini", summary, transcript)
		if err == nil {
			t.Fatal("expected the provider error")
		}
		if summary.SummarySource != AIAgentSummarySourceLocalFallback {
			t.Errorf("source = %q, want the untouched fallback", summary.SummarySource)
		}
	})

	t.Run("invalid JSON keeps the fallback", func(t *testing.T) {
		summary := base()
		if _, _, err := applyLLMSummarySynthesis(context.Background(), &fakeSummaryProvider{text: "not json"}, "gpt-4o-mini", summary, transcript); err == nil {
			t.Fatal("expected an invalid-JSON error")
		}
		if summary.SummarySource != AIAgentSummarySourceLocalFallback {
			t.Errorf("source = %q, want the untouched fallback", summary.SummarySource)
		}
	})

	t.Run("missing provider is an error", func(t *testing.T) {
		if _, _, err := applyLLMSummarySynthesis(context.Background(), nil, "gpt-4o-mini", base(), transcript); err == nil {
			t.Fatal("expected an error for a nil provider")
		}
	})
}

func TestResolveAIAgentSummarizeModel(t *testing.T) {
	service := func(provider config.ProviderType, model string) *config.ServiceConfig {
		options := map[string]interface{}{}
		if model != "" {
			options["summarize_model"] = model
		}
		return &config.ServiceConfig{Provider: provider, Options: options}
	}

	if got := resolveAIAgentSummarizeModel(nil); got != "" {
		t.Errorf("model = %q, want none without a config", got)
	}
	if got := resolveAIAgentSummarizeModel(&config.InsightsConfig{}); got != "" {
		t.Errorf("model = %q, want none without the ai_agent service", got)
	}

	cfg := &config.InsightsConfig{Services: map[insights.ServiceType]*config.ServiceConfig{
		insights.ServiceTypeAIAgent: service(config.ProviderOpenAI, "gpt-4o"),
	}}
	if got := resolveAIAgentSummarizeModel(cfg); got != "gpt-4o" {
		t.Errorf("model = %q, want the ai_agent summarize_model", got)
	}

	cfg.Services[insights.ServiceTypeAIAgent] = service(config.ProviderOpenAI, "")
	cfg.Services[insights.ServiceTypeMeetingSummarizing] = service(config.ProviderGoogle, "gemini-1.5")
	cfg.Services[insights.ServiceTypeAITextChat] = service(config.ProviderOpenAI, "gpt-4o-mini")
	if got := resolveAIAgentSummarizeModel(cfg); got != "gpt-4o-mini" {
		t.Errorf("model = %q, want the ai_text_chat fallback of the same provider", got)
	}

	// A text service of another provider must never be used with these credentials.
	delete(cfg.Services, insights.ServiceTypeAITextChat)
	if got := resolveAIAgentSummarizeModel(cfg); got != "" {
		t.Errorf("model = %q, want no cross-provider fallback", got)
	}

	cfg.Services[insights.ServiceTypeMeetingSummarizing] = service(config.ProviderOpenAI, "gpt-4o-audio")
	if got := resolveAIAgentSummarizeModel(cfg); got != "gpt-4o-audio" {
		t.Errorf("model = %q, want the meeting_summarizing fallback", got)
	}
}

func TestFinalizeSession(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	m := newAIAgentTestModel(rc)
	ctx := context.Background()

	// A room where no agent ran has nothing to summarize.
	if summary := m.FinalizeSession(ctx, 0, "room-idle", ""); summary != nil {
		t.Fatalf("summary = %+v, want nil for a room without a session", summary)
	}

	if err := m.redisService.SaveAIAgentSessionTelemetry(ctx, "room-final", redisservice.AIAgentSessionTelemetry{
		SessionID:        "session-1",
		AgentName:        "Ms. Ava",
		Topic:            "Fractions",
		AllowedSubtopics: []string{"halves", "quarters"},
		StartedAt:        1700000000000,
		EndedAt:          1700000300000,
		TurnsCount:       8,
		ToolsUsed:        map[string]int{"whiteboard_write_content": 1},
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []redisservice.AIProgressEntry{
		{Concept: "quarters", Status: "practiced"},
		{Concept: "halves", Status: "mastered"},
	} {
		if err := m.redisService.RecordAILearningProgress(ctx, "room-final", entry); err != nil {
			t.Fatal(err)
		}
	}
	newSummaryQuizPoll(t, m.redisService, rc, "room-final", "poll-1", "1/2 = 2/4?",
		&plugnmeet.CreatePollOptions{Id: 1, Text: "true"},
		&plugnmeet.CreatePollOptions{Id: 2, Text: "false"},
	)
	if err := m.redisService.SaveAIQuizMetadata(ctx, "room-final", "poll-1", 0, "equivalent"); err != nil {
		t.Fatal(err)
	}
	if err := m.redisService.AddPollResponse(&plugnmeet.SubmitPollResponseReq{
		RoomId: "room-final", PollId: "poll-1", UserId: "u1", Name: "u1",
		SelectedOptions: []uint64{1},
	}); err != nil {
		t.Fatal(err)
	}

	summary := m.FinalizeSession(ctx, 0, "room-final", "sid-final")
	if summary == nil {
		t.Fatal("no summary was built for a room with a session")
	}
	if summary.Session.RoomID != "room-final" || summary.Session.RoomSid != "sid-final" {
		t.Errorf("session = %+v, want the room identity", summary.Session)
	}
	if summary.Evaluation.MasteryLevel == "" || summary.Session.DurationSeconds != 300 {
		t.Errorf("summary = %+v, want the telemetry derived duration", summary)
	}
	// The progress hash is unordered in Redis, the artifact must not be.
	if len(summary.ProgressLog) != 2 || summary.ProgressLog[0].Concept != "halves" {
		t.Errorf("progress log = %+v, want the entries sorted by concept", summary.ProgressLog)
	}
	if summary.QuizEvaluation.TotalQuizzes != 1 || summary.QuizEvaluation.AccuracyPercent != 100 {
		t.Errorf("quiz evaluation = %+v, want the single correct answer", summary.QuizEvaluation)
	}
	if summary.SummarySource != AIAgentSummarySourceLocalFallback {
		t.Errorf("source = %q, want the local fallback without a provider", summary.SummarySource)
	}

	for _, key := range []string{aiAgentTestKey("room-final", "telemetry"), aiAgentTestKey("room-final", "progress")} {
		if exists, err := rc.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Errorf("key %q survived the finalize (exists %d, err %v)", key, exists, err)
		}
	}
}
