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
	"google.golang.org/protobuf/encoding/protojson"

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
	if question.PollId != "poll-1" || question.CorrectOption != "4" {
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
	quizzes := &plugnmeet.AIAgentSummaryQuizEvaluation{
		TotalQuizzes:     1,
		TotalResponses:   4,
		CorrectResponses: 3,
		AccuracyPercent:  75,
		Questions: []*plugnmeet.AIAgentSummaryQuizQuestion{
			{PollId: "poll-1", Question: "1/2 = 2/4?", Passed: true},
		},
	}

	summary := buildDeterministicSessionSummary("room-1", "sid-1", telemetry, progress, quizzes)

	if summary.SummarySource != AIAgentSummarySourceLocalFallback {
		t.Errorf("summary = %+v, want the local fallback source", summary)
	}
	if summary.UserInfo == nil || summary.UserInfo.GetUserId() != "" {
		t.Errorf("user info = %+v, want it present and empty without telemetry", summary.UserInfo)
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
	empty := buildDeterministicSessionSummary("room-2", "sid-2", nil, nil, nil)
	if empty == nil || empty.Session.RoomId != "room-2" {
		t.Fatalf("empty summary = %+v", empty)
	}
	if empty.Evaluation.MasteryLevel != "beginner" || empty.QuizEvaluation.Questions == nil {
		t.Errorf("empty summary = %+v, want a beginner level and an empty question list", empty.Evaluation)
	}
}

func TestApplyLLMSummarySynthesis(t *testing.T) {
	base := func() *plugnmeet.AIAgentSessionSummary {
		return buildDeterministicSessionSummary("room-1", "sid-1",
			&redisservice.AIAgentSessionTelemetry{Topic: "Fractions", TurnsCount: 4},
			[]redisservice.AIProgressEntry{{Concept: "halves", Status: "practiced"}},
			nil)
	}
	transcript := &redisservice.AIAgentSessionTelemetry{Transcript: []redisservice.AISessionTranscriptTurn{
		{Role: "participant", Text: "what is a half"},
		{Role: "agent", Text: "one of two equal parts"},
	}}

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

	t.Run("running summary reaches the prompt", func(t *testing.T) {
		provider := &fakeSummaryProvider{text: "{}"}
		telemetry := *transcript
		telemetry.RollingSummary = "Covered halves so far."

		if _, _, err := applyLLMSummarySynthesis(context.Background(), provider, "gpt-4o-mini", base(), &telemetry); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		prompt := provider.history[len(provider.history)-1].GetText()
		if !strings.Contains(prompt, "Running session summary:\nCovered halves so far.") {
			t.Errorf("prompt = %q, want the running summary", prompt)
		}
		if !strings.Contains(prompt, "participant: what is a half") {
			t.Errorf("prompt = %q, want the transcript next to it", prompt)
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

	// Other text services of the same provider are configured, but none of their
	// models may be borrowed.
	cfg := &config.InsightsConfig{Services: map[insights.ServiceType]*config.ServiceConfig{
		insights.ServiceTypeAIAgent:            service(config.ProviderOpenAI, "gpt-4o"),
		insights.ServiceTypeAITextChat:         service(config.ProviderOpenAI, "gpt-4o-mini"),
		insights.ServiceTypeMeetingSummarizing: service(config.ProviderOpenAI, "gpt-4o-audio"),
	}}
	if got := resolveAIAgentSummarizeModel(cfg); got != "gpt-4o" {
		t.Errorf("model = %q, want the ai_agent summarize_model", got)
	}

	// Without an ai_agent summarize_model there is no synthesis at all.
	cfg.Services[insights.ServiceTypeAIAgent] = service(config.ProviderOpenAI, "")
	if got := resolveAIAgentSummarizeModel(cfg); got != "" {
		t.Errorf("model = %q, want no fallback to ai_text_chat or meeting_summarizing", got)
	}

	// The same applies when the ai_agent service is not configured at all.
	delete(cfg.Services, insights.ServiceTypeAIAgent)
	if got := resolveAIAgentSummarizeModel(cfg); got != "" {
		t.Errorf("model = %q, want none without the ai_agent service", got)
	}
}

// TestAIAgentSummarizeModelUsesOnlyTheResolvedService locks in that the room's
// resolved ai_agent service is the only source, so a BYOP room without its own
// summarize_model keeps the local summary instead of borrowing the platform's.
func TestAIAgentSummarizeModelUsesOnlyTheResolvedService(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	m.appConfig = &config.AppConfig{Insights: &config.InsightsConfig{
		Services: map[insights.ServiceType]*config.ServiceConfig{
			insights.ServiceTypeAIAgent: {
				Provider: config.ProviderOpenAI,
				Options:  map[string]interface{}{"summarize_model": "gpt-4o"},
			},
			insights.ServiceTypeAITextChat: {
				Provider: config.ProviderOpenAI,
				Options:  map[string]interface{}{"summarize_model": "gpt-4o-mini"},
			},
			insights.ServiceTypeMeetingSummarizing: {
				Provider: config.ProviderOpenAI,
				Options:  map[string]interface{}{"summarize_model": "gpt-4o-audio"},
			},
		},
	}}

	// The server-configured ai_agent service, as returned by resolveAIAgentProvider.
	if got := m.aiAgentSummarizeModel(m.appConfig.Insights.Services[insights.ServiceTypeAIAgent]); got != "gpt-4o" {
		t.Errorf("model = %q, want the ai_agent summarize_model", got)
	}
	// Without a resolved service the server configuration is read directly.
	if got := m.aiAgentSummarizeModel(nil); got != "gpt-4o" {
		t.Errorf("model = %q, want the server ai_agent summarize_model", got)
	}

	// A BYOP room's own service config never falls back to the platform config.
	byop := &config.ServiceConfig{
		Provider: config.ProviderGoogle,
		Options:  map[string]interface{}{"realtime_model": "gemini-live"},
	}
	if got := m.aiAgentSummarizeModel(byop); got != "" {
		t.Errorf("model = %q, want no fallback to the platform ai_agent service", got)
	}

	byop.Options["summarize_model"] = " gemini-summary "
	if got := m.aiAgentSummarizeModel(byop); got != "gemini-summary" {
		t.Errorf("model = %q, want the trimmed BYOP summarize_model", got)
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
	if summary.Session.RoomId != "room-final" || summary.Session.RoomSid != "sid-final" {
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

// TestAIAgentSessionLearnerTelemetry locks in that the learner recorded in the
// telemetry wins: it is the only identity that survives a participant leaving
// the room before the summary is built.
func TestAIAgentSessionLearnerTelemetry(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	m := newAIAgentTestModel(rc)
	ctx := context.Background()

	telemetry := &redisservice.AIAgentSessionTelemetry{
		UserID:    "learner-1",
		UserName:  "Learner One",
		ExUserID:  "external-1",
		ExtraData: `{"grade":"4"}`,
	}
	// The identity must survive the Redis round trip of the telemetry.
	if err := m.redisService.SaveAIAgentSessionTelemetry(ctx, "room-learner", *telemetry); err != nil {
		t.Fatal(err)
	}
	stored, err := m.redisService.GetAIAgentSessionTelemetry(ctx, "room-learner")
	if err != nil || stored == nil {
		t.Fatalf("stored telemetry = %+v, err %v", stored, err)
	}
	if stored.UserID != "learner-1" || stored.UserName != "Learner One" || stored.ExUserID != "external-1" || stored.ExtraData != `{"grade":"4"}` {
		t.Fatalf("stored learner = %+v, want the whole identity", stored)
	}

	info := m.aiAgentSessionLearner(ctx, "room-learner", stored)
	if info.GetUserId() != "learner-1" || info.GetName() != "Learner One" ||
		info.GetExUserId() != "external-1" || info.GetExtraData() != `{"grade":"4"}` {
		t.Fatalf("learner info = %+v, want the identity of the telemetry", info)
	}

	// Without NATS and telemetry the delegate is the only source left.
	if err := rc.Set(ctx, aiAgentTestKey("room-delegate", "delegate"), "learner-2", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got := m.aiAgentSessionLearner(ctx, "room-delegate", nil); got.GetUserId() != "learner-2" {
		t.Fatalf("learner = %+v, want the stored delegate", got)
	}

	// A room without any trace reports no learner instead of a wrong one.
	if got := m.aiAgentSessionLearner(ctx, "room-empty", nil); got.GetUserId() != "" {
		t.Fatalf("learner = %+v, want no learner", got)
	}
}

// TestAIAgentBreakoutUserId pins the per-user breakout room naming convention
// used to recover the learner of a session whose participant already left.
func TestAIAgentBreakoutUserId(t *testing.T) {
	cases := []struct {
		name     string
		roomId   string
		expected string
	}{
		{"per-user room", "parent-room-learner-1", "learner-1"},
		{"numbered room", "parent-room-2", ""},
		{"other parent", "other-room-learner-1", ""},
		{"the parent itself", "parent-room", ""},
		{"empty suffix", "parent-room-", ""},
	}
	for _, c := range cases {
		if got := aiAgentBreakoutUserId("parent-room", c.roomId); got != c.expected {
			t.Errorf("%s: learner = %q, want %q", c.name, got, c.expected)
		}
	}
}

// TestAIAgentBreakoutUserInfo covers the Redis record that still names the
// participant of a per-user session after their NATS entry is gone.
func TestAIAgentBreakoutUserInfo(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	m := newAIAgentTestModel(rc)

	room := &plugnmeet.BreakoutRoom{
		Id:    "parent-room-learner-1",
		Title: "Learner One",
		Users: []*plugnmeet.BreakoutRoomUser{{Id: "learner-1", Name: "Learner One"}},
	}
	encoded, err := protojson.Marshal(room)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.redisService.InsertOrUpdateBreakoutRoom("parent-room", room.Id, encoded); err != nil {
		t.Fatal(err)
	}

	info := m.aiAgentBreakoutUserInfo("parent-room", room.Id, "")
	if info.GetUserId() != "learner-1" || info.GetName() != "Learner One" {
		t.Fatalf("breakout learner = %+v, want learner-1 / Learner One", info)
	}
	// A different participant must not be attributed to another session.
	if info := m.aiAgentBreakoutUserInfo("parent-room", room.Id, "learner-9"); info != nil {
		t.Fatalf("breakout learner = %+v, want none for an unassigned user", info)
	}
	if info := m.aiAgentBreakoutUserInfo("unknown-parent", room.Id, ""); info != nil {
		t.Fatalf("breakout learner = %+v, want none without a record", info)
	}
}

// TestMergeAIAgentUserInfo locks in that a later partial lookup never erases a
// known identity.
func TestMergeAIAgentUserInfo(t *testing.T) {
	info := &plugnmeet.RoomArtifactUserInfo{UserId: "learner-1", Name: optionalString("Learner One")}
	mergeAIAgentUserInfo(info, nil)
	mergeAIAgentUserInfo(info, &plugnmeet.RoomArtifactUserInfo{
		UserId:    "other",
		Name:      optionalString("Other"),
		ExUserId:  optionalString("external-1"),
		ExtraData: optionalString(`{"grade":"4"}`),
	})

	if info.GetUserId() != "learner-1" || info.GetName() != "Learner One" {
		t.Fatalf("info = %+v, want the already known identity", info)
	}
	if info.GetExUserId() != "external-1" || info.GetExtraData() != `{"grade":"4"}` {
		t.Fatalf("info = %+v, want the missing fields filled", info)
	}
}

// TestApplyAIAgentSessionLearner locks in that the artifact's session block
// carries the resolved learner.
func TestApplyAIAgentSessionLearner(t *testing.T) {
	summary := buildDeterministicSessionSummary("room-1", "sid-1", nil, nil, nil)
	applyAIAgentSessionLearner(summary, &plugnmeet.RoomArtifactUserInfo{
		UserId:    "learner-1",
		Name:      optionalString("Learner One"),
		ExUserId:  optionalString("external-1"),
		ExtraData: optionalString(`{"grade":"4"}`),
	})

	if summary.GetUserInfo().GetUserId() != "learner-1" || summary.GetUserInfo().GetName() != "Learner One" {
		t.Fatalf("user info = %+v, want the learner identity", summary.GetUserInfo())
	}
	if summary.GetUserInfo().GetExUserId() != "external-1" {
		t.Fatalf("user info = %+v, want the external id", summary.GetUserInfo())
	}
	if summary.GetUserInfo().GetExtraData() != `{"grade":"4"}` {
		t.Fatalf("user info = %+v, want the extra data", summary.GetUserInfo())
	}

	// A missing learner still leaves an empty, non-nil user_info behind.
	untouched := buildDeterministicSessionSummary("room-2", "sid-2", nil, nil, nil)
	applyAIAgentSessionLearner(untouched, &plugnmeet.RoomArtifactUserInfo{})
	if untouched.GetUserInfo() == nil || untouched.GetUserInfo().GetUserId() != "" {
		t.Fatalf("user info = %+v, want an empty but present identity", untouched.GetUserInfo())
	}
}

// TestMarshalAIAgentSessionSummary locks in the artifact JSON: proto names,
// zero values, empty lists and empty maps are emitted, while unset optional
// fields stay omitted.
func TestMarshalAIAgentSessionSummary(t *testing.T) {
	summary := buildDeterministicSessionSummary("room-1", "sid-1",
		&redisservice.AIAgentSessionTelemetry{
			SessionID:        "session-1",
			UserID:           "learner-1",
			Topic:            "Fractions",
			AllowedSubtopics: []string{"halves"},
			StartedAt:        1700000000000,
			EndedAt:          1700000600000,
			ToolsUsed:        map[string]int{"whiteboard_write_content": 2},
		},
		[]redisservice.AIProgressEntry{{Concept: "halves", Status: "mastered"}},
		nil,
	)
	applyAIAgentSessionLearner(summary, &plugnmeet.RoomArtifactUserInfo{
		UserId: "learner-1",
		Name:   optionalString("Learner One"),
	})

	content, err := marshalAIAgentSessionSummary(summary)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	raw := string(content)
	for _, want := range []string{
		`"summary_source"`,
		`"generated_at"`,
		`"user_info"`,
		`"user_id"`,
		`"name"`,
		`"session"`,
		`"room_id"`,
		`"allowed_subtopics"`,
		`"tools_used"`,
		`"whiteboard_used"`,
		`"notepad_used"`,
		`"concepts_practiced"`,
		`"questions"`,
		`"unfinished_subtopics"`,
		`"narrative_summary"`,
		`"usage":`,
		`"local_fallback"`,
		`"learner-1"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("summary JSON does not contain %s:\n%s", want, raw)
		}
	}
	// The learner sits at the top level, right before the session it describes.
	if info, session := strings.Index(raw, `"user_info"`), strings.Index(raw, `"session"`); info < 0 || session < 0 || info > session {
		t.Errorf("user_info must come before session:\n%s", raw)
	}
	// The structure is the protobuf, so no schema version and no optional
	// duplicates of the identity are serialized.
	for _, unwanted := range []string{`"schema_version"`, `"user_name"`, `"ex_user_id"`, `"notes"`, `"null"`} {
		if strings.Contains(raw, unwanted) {
			t.Errorf("summary JSON must not contain %s:\n%s", unwanted, raw)
		}
	}

	// The artifact must round trip into the protocol type.
	decoded := new(plugnmeet.AIAgentSessionSummary)
	if err = protojson.Unmarshal(content, decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if decoded.GetUserInfo().GetUserId() != "learner-1" || decoded.GetUserInfo().GetName() != "Learner One" {
		t.Fatalf("decoded summary = %+v, want the learner identity at the top level", decoded)
	}
	if decoded.GetWorkspaceActivity().GetToolsUsed()["whiteboard_write_content"] != 2 {
		t.Fatalf("decoded tools used = %+v, want the whiteboard counter", decoded.GetWorkspaceActivity().GetToolsUsed())
	}
	if decoded.GetWorkspaceActivity().GetNotepadUsed() {
		t.Error("notepad must stay unused when the agent never used it")
	}
	if len(decoded.GetEvaluation().GetConceptsPracticed()) != 0 || len(decoded.GetQuizEvaluation().GetQuestions()) != 0 {
		t.Errorf("decoded summary = %+v, want the empty lists kept", decoded)
	}

	// A session without a learner keeps an empty user_info instead of dropping it.
	anonymous, err := marshalAIAgentSessionSummary(buildDeterministicSessionSummary("room-2", "sid-2", nil, nil, nil))
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if anon := string(anonymous); !strings.Contains(anon, `"user_info"`) ||
		strings.Index(anon, `"user_info"`) > strings.Index(anon, `"session"`) {
		t.Errorf("anonymous summary must keep an empty user_info before the session:\n%s", anon)
	}
	for _, unwanted := range []string{`"schema_version"`, `"user_name"`, `"ex_user_id"`} {
		if strings.Contains(string(anonymous), unwanted) {
			t.Errorf("anonymous summary must not contain %s:\n%s", unwanted, anonymous)
		}
	}
	if !strings.Contains(string(anonymous), `"tools_used"`) || !strings.Contains(string(anonymous), `{}`) {
		t.Errorf("anonymous summary must keep the empty tool map:\n%s", anonymous)
	}
}
