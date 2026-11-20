package redisservice

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAIKnowledgeDocumentRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	url, other := "https://docs.example.com/plans", "https://docs.example.com/pricing"

	if _, found, err := s.GetAIKnowledgeDocument(ctx, "room-kb", url); err != nil || found {
		t.Fatalf("a cold cache must miss (found %v, err %v)", found, err)
	}

	key := AiAgentKnowledgeCacheKey("room-kb", url)
	if want := AiAgentRedisKey + ":room-kb:kb:"; !strings.HasPrefix(key, want) {
		t.Errorf("knowledge key %q must start with %q", key, want)
	}
	if key == AiAgentKnowledgeCacheKey("room-kb", other) || key != AiAgentKnowledgeCacheKey("room-kb", url) {
		t.Error("the key must be stable per url and distinct across urls")
	}

	ttl := 5 * time.Minute
	if err := s.SetAIKnowledgeDocument(ctx, "room-kb", url, `{"text":"hello"}`, ttl); err != nil {
		t.Fatal(err)
	}
	value, found, err := s.GetAIKnowledgeDocument(ctx, "room-kb", url)
	if err != nil || !found || value != `{"text":"hello"}` {
		t.Fatalf("value = %q, found %v, err %v", value, found, err)
	}
	if left, err := rc.TTL(ctx, key).Result(); err != nil || left > ttl || left <= 0 {
		t.Errorf("ttl = %s (err %v), want at most %s", left, err, ttl)
	}
	if _, found, _ := s.GetAIKnowledgeDocument(ctx, "room-kb", other); found {
		t.Error("another url must not hit the cache")
	}
}

func TestAIQuizMetadataRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()

	if _, _, found, err := s.GetAIQuizMetadata(ctx, "room-quiz", "poll-1"); err != nil || found {
		t.Fatalf("a missing quiz must report found=false (found %v, err %v)", found, err)
	}

	if err := s.SaveAIQuizMetadata(ctx, "room-quiz", "poll-1", 2, "went is irregular"); err != nil {
		t.Fatal(err)
	}
	index, explanation, found, err := s.GetAIQuizMetadata(ctx, "room-quiz", "poll-1")
	if err != nil || !found || index != 2 || explanation != "went is irregular" {
		t.Fatalf("metadata = (%d, %q, %v, %v)", index, explanation, found, err)
	}
	if left, err := rc.TTL(ctx, aiAgentQuizKey("room-quiz", "poll-1")).Result(); err != nil || left > s.defaultTTL || left <= 0 {
		t.Errorf("ttl = %s (err %v), want at most %s", left, err, s.defaultTTL)
	}
	if _, _, found, _ := s.GetAIQuizMetadata(ctx, "room-quiz", "poll-2"); found {
		t.Error("another poll must not resolve the answer key")
	}
}

func TestAILearningProgressRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()

	if entries, err := s.GetAILearningProgress(ctx, "room-progress"); err != nil || len(entries) != 0 {
		t.Fatalf("empty room = %+v (err %v)", entries, err)
	}

	first := AIProgressEntry{Concept: "past tense", Status: "practicing", Notes: "mixes went/goed"}
	second := AIProgressEntry{Concept: "articles", Status: "introduced"}
	for _, entry := range []AIProgressEntry{first, second} {
		if err := s.RecordAILearningProgress(ctx, "room-progress", entry); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := s.GetAILearningProgress(ctx, "room-progress")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v (err %v)", entries, err)
	}
	if got := entries["past tense"]; got != first {
		t.Errorf("entry = %+v, want %+v", got, first)
	}

	// A later observation of the same concept replaces the earlier one.
	mastered := AIProgressEntry{Concept: "past tense", Status: "mastered"}
	if err := s.RecordAILearningProgress(ctx, "room-progress", mastered); err != nil {
		t.Fatal(err)
	}
	if entries, _ := s.GetAILearningProgress(ctx, "room-progress"); len(entries) != 2 || entries["past tense"].Status != "mastered" {
		t.Errorf("entries = %+v, want the concept updated in place", entries)
	}
	if left, err := rc.TTL(ctx, aiAgentProgressKey("room-progress")).Result(); err != nil || left <= 0 {
		t.Errorf("the progress hash must carry a ttl (left %s, err %v)", left, err)
	}
	if entries, _ := s.GetAILearningProgress(ctx, "room-other"); len(entries) != 0 {
		t.Error("another room must not see the progress")
	}
}

func TestAIAgentSessionTelemetryRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()

	if telemetry, err := s.GetAIAgentSessionTelemetry(ctx, "room-telemetry"); err != nil || telemetry != nil {
		t.Fatalf("a room without a session must report nil (telemetry %+v, err %v)", telemetry, err)
	}
	if want := AiAgentRedisKey + ":room-telemetry:telemetry"; aiAgentTelemetryKey("room-telemetry") != want {
		t.Errorf("telemetry key = %q, want %q", aiAgentTelemetryKey("room-telemetry"), want)
	}

	want := AIAgentSessionTelemetry{
		SessionID:              "session-1",
		AgentName:              "Ms. Ava",
		Topic:                  "Grade 4 Fractions",
		AllowedSubtopics:       []string{"equivalent fractions"},
		StartedAt:              1700000000000,
		EndedAt:                1700000060000,
		EndReason:              "budget",
		TurnsCount:             7,
		OffTopicRedirectsCount: 2,
		InputTokens:            1234,
		OutputTokens:           567,
		AudioMillis:            42000,
		ToolsUsed:              map[string]int{"whiteboard_write_content": 3},
		Transcript: []AISessionTranscriptTurn{
			{Role: "participant", Text: "what is a quarter"},
			{Role: "agent", Text: "one of four equal parts"},
		},
	}
	if err := s.SaveAIAgentSessionTelemetry(ctx, "room-telemetry", want); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetAIAgentSessionTelemetry(ctx, "room-telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("the saved telemetry was not found")
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("telemetry = %+v, want %+v", *got, want)
	}
	if left, err := rc.TTL(ctx, aiAgentTelemetryKey("room-telemetry")).Result(); err != nil || left > s.defaultTTL || left <= 0 {
		t.Errorf("telemetry ttl = %s (err %v), want at most %s", left, err, s.defaultTTL)
	}

	// A later flush replaces the previous snapshot rather than piling up.
	updated := want
	updated.TurnsCount = 9
	updated.Transcript = append(updated.Transcript, AISessionTranscriptTurn{Role: "participant", Text: "and a half?"})
	if err := s.SaveAIAgentSessionTelemetry(ctx, "room-telemetry", updated); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAIAgentSessionTelemetry(ctx, "room-telemetry"); got == nil || got.TurnsCount != 9 || len(got.Transcript) != 3 {
		t.Fatalf("telemetry was not replaced: %+v", got)
	}
	if got, _ := s.GetAIAgentSessionTelemetry(ctx, "room-other"); got != nil {
		t.Error("another room must not see the telemetry")
	}
}

func TestDeleteAIAgentSessionData(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()

	if err := s.SaveAIAgentSessionTelemetry(ctx, "room-flush", AIAgentSessionTelemetry{SessionID: "session-1", TurnsCount: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAILearningProgress(ctx, "room-flush", AIProgressEntry{Concept: "past tense", Status: "practicing"}); err != nil {
		t.Fatal(err)
	}
	keys := []string{aiAgentTelemetryKey("room-flush"), aiAgentProgressKey("room-flush")}
	if exists, err := rc.Exists(ctx, keys...).Result(); err != nil || exists != 2 {
		t.Fatalf("expected both session keys before the delete (exists %d, err %v)", exists, err)
	}

	s.DeleteAIAgentSessionData(ctx, "room-flush")

	if exists, err := rc.Exists(ctx, keys...).Result(); err != nil || exists != 0 {
		t.Fatalf("session keys survived the delete (exists %d, err %v)", exists, err)
	}
	if telemetry, err := s.GetAIAgentSessionTelemetry(ctx, "room-flush"); err != nil || telemetry != nil {
		t.Fatalf("telemetry survived the delete (%+v, err %v)", telemetry, err)
	}
	if entries, err := s.GetAILearningProgress(ctx, "room-flush"); err != nil || len(entries) != 0 {
		t.Fatalf("progress survived the delete (%+v, err %v)", entries, err)
	}

	// Deleting a room that never had a session is a no-op.
	s.DeleteAIAgentSessionData(ctx, "room-absent")
}
