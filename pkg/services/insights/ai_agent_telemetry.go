package insightsservice

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

func (a *AIAgent) recordUserTranscript(ev insights.RealtimeEvent) {
	if !ev.Final || strings.TrimSpace(ev.Text) == "" {
		return
	}
	a.logger.WithField("text", ev.Text).Debug("ai agent: participant transcript")
	a.recordTranscriptTurn(transcriptRoleParticipant, ev.Text)
}

// recordTranscriptTurn keeps the tail of the conversation and counts the
// participant's turns; partial results never reach it.
func (a *AIAgent) recordTranscriptTurn(role, text string) {
	text = truncateRunes(strings.TrimSpace(text), maxTranscriptTurnChars)
	if text == "" {
		return
	}

	a.stateMu.Lock()
	a.state.transcript = append(a.state.transcript, redisservice.AISessionTranscriptTurn{Role: role, Text: text})
	a.state.totalTranscriptTurns++
	if excess := len(a.state.transcript) - maxSessionTranscriptTurns; excess > 0 {
		kept := make([]redisservice.AISessionTranscriptTurn, maxSessionTranscriptTurns)
		copy(kept, a.state.transcript[excess:])
		a.state.transcript = kept
	}
	if role == transcriptRoleParticipant {
		a.state.turnsCount++
	}
	pending := a.state.totalTranscriptTurns - a.state.summarizedTurns
	a.stateMu.Unlock()

	a.requestSummary(pending)
}

// requestSummary rolls the running summary forward once enough unsummarized
// turns have accumulated, one background summary at a time.
func (a *AIAgent) requestSummary(pending int) {
	if a.summarize == nil || pending < a.summarizeWindow {
		return
	}
	if !a.summarizing.CompareAndSwap(false, true) {
		return
	}

	// Stop waits for this group, so a turn arriving from a chat handler can never
	// join it once the session is over.
	a.summarizeMu.Lock()
	if a.summarizeStop {
		a.summarizeMu.Unlock()
		a.summarizing.Store(false)
		return
	}
	a.summarizeWg.Add(1)
	a.summarizeMu.Unlock()

	go func() {
		defer a.summarizeWg.Done()
		defer a.summarizing.Store(false)
		a.summarizeConversation()
	}()
}

// summarizeConversation folds the unsummarized turns into the running summary.
// The reported tokens are recorded even when the model fails, so the usage stays
// exact.
func (a *AIAgent) summarizeConversation() {
	a.stateMu.Lock()
	prevSummary := strings.TrimSpace(a.state.rollingSummary)
	targetTurnCount := a.state.totalTranscriptTurns
	pending := targetTurnCount - a.state.summarizedTurns
	transcript := append([]redisservice.AISessionTranscriptTurn(nil), a.state.transcript...)
	a.stateMu.Unlock()

	recent := recentTranscriptTurns(transcript, pending, a.summarizeWindow)
	if len(recent) == 0 {
		return
	}

	history := make([]*plugnmeet.InsightsAITextChatContent, 0, len(recent)+1)
	if prevSummary != "" {
		history = append(history, &plugnmeet.InsightsAITextChatContent{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM,
			Text: "This is the running summary of the session so far: " + prevSummary,
		})
	}
	for _, turn := range recent {
		role := plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER
		if turn.Role == transcriptRoleAgent {
			role = plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL
		}
		history = append(history, &plugnmeet.InsightsAITextChatContent{Role: role, Text: turn.Text})
	}

	sumCtx, cancel := context.WithTimeout(a.ctx, aiAgentSummarizeTimeout)
	defer cancel()
	summary, promptTokens, completionTokens, err := a.summarize(sumCtx, history)

	a.stateMu.Lock()
	a.state.summaryInputTokens += uint64(promptTokens)
	a.state.summaryOutputTokens += uint64(completionTokens)
	updated := err == nil && strings.TrimSpace(summary) != ""
	if updated {
		a.state.rollingSummary = strings.TrimSpace(summary)
		a.state.summarizedTurns = targetTurnCount
	}
	a.stateMu.Unlock()

	if err != nil {
		a.logger.WithError(err).Debug("ai agent: the rolling summary failed")
		return
	}
	if updated {
		// A crash must not lose the summary the session already paid for.
		a.persistTelemetry("")
	}
}

// recentTranscriptTurns returns the tail of turns the summary still has to
// cover, bounded by the window and by the turns the ring buffer kept.
func recentTranscriptTurns(transcript []redisservice.AISessionTranscriptTurn, pending, window int) []redisservice.AISessionTranscriptTurn {
	if pending <= 0 || len(transcript) == 0 {
		return nil
	}
	if pending > len(transcript) {
		pending = len(transcript)
	}
	if window > 0 && pending > window {
		pending = window
	}
	return transcript[len(transcript)-pending:]
}

// recordToolUse counts the tools the agent actually ran. The off-topic sentinel
// is reported separately as a redirect.
func (a *AIAgent) recordToolUse(toolName string) {
	if toolName == "" || toolName == toolNameReportOffTopic {
		return
	}
	a.stateMu.Lock()
	a.state.toolsUsed[toolName]++
	a.stateMu.Unlock()
}

// TelemetrySnapshot is the session's state at this instant. An empty endReason
// means the session is still running, so EndedAt stays zero.
func (a *AIAgent) TelemetrySnapshot(endReason string) redisservice.AIAgentSessionTelemetry {
	a.stateMu.Lock()
	state := a.state
	toolsUsed := make(map[string]int, len(state.toolsUsed))
	for name, count := range state.toolsUsed {
		toolsUsed[name] = count
	}
	transcript := append([]redisservice.AISessionTranscriptTurn(nil), state.transcript...)
	a.stateMu.Unlock()

	learner := a.Learner()

	var endedAt int64
	if endReason != "" {
		endedAt = time.Now().UnixMilli()
	}

	return redisservice.AIAgentSessionTelemetry{
		SessionID:              a.sessionId,
		UserID:                 learner.UserID,
		UserName:               learner.UserName,
		ExUserID:               learner.ExUserID,
		ExtraData:              learner.ExtraData,
		AgentName:              a.agentName,
		Topic:                  a.boundary.Topic,
		AllowedSubtopics:       append([]string(nil), a.boundary.AllowedSubtopics...),
		StartedAt:              a.startedAt.UnixMilli(),
		EndedAt:                endedAt,
		EndReason:              endReason,
		TurnsCount:             state.turnsCount,
		OffTopicRedirectsCount: a.guard.OffTopicRedirectsCount(),
		InputTokens:            uint64(state.inputTokens),
		OutputTokens:           uint64(state.outputTokens),
		CachedInputTokens:      state.cachedInputTokens,
		TextInputTokens:        state.textInputTokens,
		AudioInputTokens:       state.audioInputTokens,
		ImageInputTokens:       state.imageInputTokens,
		CachedTextInputTokens:  state.cachedTextInputTokens,
		CachedAudioInputTokens: state.cachedAudioInputTokens,
		CachedImageInputTokens: state.cachedImageInputTokens,
		TextOutputTokens:       state.textOutputTokens,
		AudioOutputTokens:      state.audioOutputTokens,
		ThinkingOutputTokens:   state.thinkingOutputTokens,

		CacheWriteInputTokens:      state.cacheWriteInputTokens,
		CacheWriteTextInputTokens:  state.cacheWriteTextInputTokens,
		CacheWriteAudioInputTokens: state.cacheWriteAudioInputTokens,
		CacheWriteImageInputTokens: state.cacheWriteImageInputTokens,

		DelegationInputTokens:                uint64(state.delegationInputTokens),
		DelegationOutputTokens:               uint64(state.delegationOutputTokens),
		DelegationCachedInputTokens:          state.delegationCachedInputTokens,
		DelegationCacheWriteInputTokens:      state.delegationCacheWriteInputTokens,
		DelegationTextInputTokens:            state.delegationTextInputTokens,
		DelegationAudioInputTokens:           state.delegationAudioInputTokens,
		DelegationImageInputTokens:           state.delegationImageInputTokens,
		DelegationCachedTextInputTokens:      state.delegationCachedTextInputTokens,
		DelegationCachedAudioInputTokens:     state.delegationCachedAudioInputTokens,
		DelegationCachedImageInputTokens:     state.delegationCachedImageInputTokens,
		DelegationCacheWriteTextInputTokens:  state.delegationCacheWriteTextInputTokens,
		DelegationCacheWriteAudioInputTokens: state.delegationCacheWriteAudioInputTokens,
		DelegationCacheWriteImageInputTokens: state.delegationCacheWriteImageInputTokens,
		DelegationTextOutputTokens:           state.delegationTextOutputTokens,
		DelegationAudioOutputTokens:          state.delegationAudioOutputTokens,
		DelegationThinkingOutputTokens:       state.delegationThinkingOutputTokens,

		AudioMillis:          state.audioMillis,
		AvatarMillis:         a.avatarMillis(),
		AvatarProvider:       a.avatarProvider,
		CustomAvatarProvider: a.customAvatarProvider,
		ToolsUsed:            toolsUsed,
		Transcript:           transcript,
		RollingSummary:       state.rollingSummary,
		SummarizedTurns:      state.summarizedTurns,
		TotalTranscriptTurns: state.totalTranscriptTurns,
		SummaryInputTokens:   state.summaryInputTokens,
		SummaryOutputTokens:  state.summaryOutputTokens,

		CustomProvider: a.customProvider,
	}
}

// avatarMillis is the avatar duration billed to the room: the current session
// so far plus the earlier session of the same room. Without a running avatar it
// is only what the earlier sessions already used.
func (a *AIAgent) avatarMillis() uint64 {
	if a.avatarStartedAt.IsZero() {
		return a.priorAvatarMillis
	}
	elapsed := time.Since(a.avatarStartedAt)
	if elapsed < 0 {
		return a.priorAvatarMillis
	}
	return a.priorAvatarMillis + uint64(elapsed/time.Millisecond)
}

// seedStateFromTelemetry continues the session an earlier agent left behind in
// the same room, so a restart keeps its conversation and its cumulative usage.
func (a *AIAgent) seedStateFromTelemetry(t *redisservice.AIAgentSessionTelemetry) {
	if t == nil {
		return
	}

	// The avatar duration carries over, so a restart continues the budget of the
	// room instead of granting a fresh one. An avatar configured for this
	// session wins over the provider the earlier session recorded.
	a.priorAvatarMillis = t.AvatarMillis
	if a.avatarProvider == "" {
		a.avatarProvider = t.AvatarProvider
		a.customAvatarProvider = t.CustomAvatarProvider
	}

	a.stateMu.Lock()
	defer a.stateMu.Unlock()

	state := &a.state
	state.turnsCount = t.TurnsCount
	state.inputTokens = clampUint32(t.InputTokens)
	state.outputTokens = clampUint32(t.OutputTokens)
	state.cachedInputTokens = t.CachedInputTokens
	state.textInputTokens = t.TextInputTokens
	state.audioInputTokens = t.AudioInputTokens
	state.imageInputTokens = t.ImageInputTokens
	state.cachedTextInputTokens = t.CachedTextInputTokens
	state.cachedAudioInputTokens = t.CachedAudioInputTokens
	state.cachedImageInputTokens = t.CachedImageInputTokens
	state.textOutputTokens = t.TextOutputTokens
	state.audioOutputTokens = t.AudioOutputTokens
	state.thinkingOutputTokens = t.ThinkingOutputTokens
	state.cacheWriteInputTokens = t.CacheWriteInputTokens
	state.cacheWriteTextInputTokens = t.CacheWriteTextInputTokens
	state.cacheWriteAudioInputTokens = t.CacheWriteAudioInputTokens
	state.cacheWriteImageInputTokens = t.CacheWriteImageInputTokens
	state.delegationInputTokens = clampUint32(t.DelegationInputTokens)
	state.delegationOutputTokens = clampUint32(t.DelegationOutputTokens)
	state.delegationCachedInputTokens = t.DelegationCachedInputTokens
	state.delegationCacheWriteInputTokens = t.DelegationCacheWriteInputTokens
	state.delegationTextInputTokens = t.DelegationTextInputTokens
	state.delegationAudioInputTokens = t.DelegationAudioInputTokens
	state.delegationImageInputTokens = t.DelegationImageInputTokens
	state.delegationCachedTextInputTokens = t.DelegationCachedTextInputTokens
	state.delegationCachedAudioInputTokens = t.DelegationCachedAudioInputTokens
	state.delegationCachedImageInputTokens = t.DelegationCachedImageInputTokens
	state.delegationCacheWriteTextInputTokens = t.DelegationCacheWriteTextInputTokens
	state.delegationCacheWriteAudioInputTokens = t.DelegationCacheWriteAudioInputTokens
	state.delegationCacheWriteImageInputTokens = t.DelegationCacheWriteImageInputTokens
	state.delegationTextOutputTokens = t.DelegationTextOutputTokens
	state.delegationAudioOutputTokens = t.DelegationAudioOutputTokens
	state.delegationThinkingOutputTokens = t.DelegationThinkingOutputTokens
	state.audioMillis = t.AudioMillis
	state.summaryInputTokens = t.SummaryInputTokens
	state.summaryOutputTokens = t.SummaryOutputTokens

	state.toolsUsed = make(map[string]int, len(t.ToolsUsed))
	for name, count := range t.ToolsUsed {
		state.toolsUsed[name] = count
	}
	state.transcript = append([]redisservice.AISessionTranscriptTurn(nil), t.Transcript...)
	state.rollingSummary = t.RollingSummary
	state.summarizedTurns = t.SummarizedTurns
	state.totalTranscriptTurns = t.TotalTranscriptTurns
	if state.totalTranscriptTurns < len(state.transcript) {
		state.totalTranscriptTurns = len(state.transcript)
	}
}

// clampUint32 keeps a stored counter within the runtime type; a session never
// reaches the limit in practice.
func clampUint32(v uint64) uint32 {
	if v > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(v)
}

// buildReconnectHandoff renders the context a reconnected or restarted session
// opens with, so the agent continues the conversation instead of starting over.
func (a *AIAgent) buildReconnectHandoff() string {
	a.stateMu.Lock()
	rollingSummary := a.state.rollingSummary
	transcript := append([]redisservice.AISessionTranscriptTurn(nil), a.state.transcript...)
	unsummarized := a.state.totalTranscriptTurns - a.state.summarizedTurns
	a.stateMu.Unlock()

	return BuildSessionReconnectHandoff(rollingSummary, transcript, unsummarized)
}

// persistTelemetry writes the snapshot with a context of its own: Stop cancels
// a.ctx first, and the flush must still land.
func (a *AIAgent) persistTelemetry(endReason string) {
	if a.redisService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), aiAgentTelemetryTimeout)
	defer cancel()

	if err := a.redisService.SaveAIAgentSessionTelemetry(ctx, a.roomId, a.TelemetrySnapshot(endReason)); err != nil {
		a.logger.WithError(err).Debug("ai agent: failed to save the session telemetry")
	}
}

// logUsage reports the session's cost accounting once the agent stopped.
func (a *AIAgent) logUsage() {
	a.stateMu.Lock()
	state := a.state
	a.stateMu.Unlock()

	a.logger.WithFields(logrus.Fields{
		"inputTokens":            state.inputTokens,
		"outputTokens":           state.outputTokens,
		"cachedInputTokens":      state.cachedInputTokens,
		"cacheWriteInputTokens":  state.cacheWriteInputTokens,
		"delegationInputTokens":  state.delegationInputTokens,
		"delegationOutputTokens": state.delegationOutputTokens,
		"audioMillis":            state.audioMillis,
		"avatarMillis":           a.avatarMillis(),
		"avatarProvider":         a.avatarProvider,
		"durationMs":             time.Since(a.startedAt).Milliseconds(),
	}).Info("ai agent: session usage")
}

func (a *AIAgent) recordUsage(ev insights.RealtimeEvent) {
	a.stateMu.Lock()
	a.state.inputTokens += ev.InputTokens
	a.state.outputTokens += ev.OutputTokens
	a.state.cachedInputTokens += uint64(ev.CachedInputTokens)
	a.state.textInputTokens += uint64(ev.TextInputTokens)
	a.state.audioInputTokens += uint64(ev.AudioInputTokens)
	a.state.imageInputTokens += uint64(ev.ImageInputTokens)
	a.state.cachedTextInputTokens += uint64(ev.CachedTextInputTokens)
	a.state.cachedAudioInputTokens += uint64(ev.CachedAudioInputTokens)
	a.state.cachedImageInputTokens += uint64(ev.CachedImageInputTokens)
	a.state.textOutputTokens += uint64(ev.TextOutputTokens)
	a.state.audioOutputTokens += uint64(ev.AudioOutputTokens)
	a.state.thinkingOutputTokens += uint64(ev.ThinkingOutputTokens)
	a.state.cacheWriteInputTokens += uint64(ev.CacheWriteInputTokens)
	a.state.cacheWriteTextInputTokens += uint64(ev.CacheWriteTextInputTokens)
	a.state.cacheWriteAudioInputTokens += uint64(ev.CacheWriteAudioInputTokens)
	a.state.cacheWriteImageInputTokens += uint64(ev.CacheWriteImageInputTokens)
	a.state.delegationInputTokens += ev.DelegationInputTokens
	a.state.delegationOutputTokens += ev.DelegationOutputTokens
	a.state.delegationCachedInputTokens += uint64(ev.DelegationCachedInputTokens)
	a.state.delegationCacheWriteInputTokens += uint64(ev.DelegationCacheWriteInputTokens)
	a.state.delegationTextInputTokens += uint64(ev.DelegationTextInputTokens)
	a.state.delegationAudioInputTokens += uint64(ev.DelegationAudioInputTokens)
	a.state.delegationImageInputTokens += uint64(ev.DelegationImageInputTokens)
	a.state.delegationCachedTextInputTokens += uint64(ev.DelegationCachedTextInputTokens)
	a.state.delegationCachedAudioInputTokens += uint64(ev.DelegationCachedAudioInputTokens)
	a.state.delegationCachedImageInputTokens += uint64(ev.DelegationCachedImageInputTokens)
	a.state.delegationCacheWriteTextInputTokens += uint64(ev.DelegationCacheWriteTextInputTokens)
	a.state.delegationCacheWriteAudioInputTokens += uint64(ev.DelegationCacheWriteAudioInputTokens)
	a.state.delegationCacheWriteImageInputTokens += uint64(ev.DelegationCacheWriteImageInputTokens)
	a.state.delegationTextOutputTokens += uint64(ev.DelegationTextOutputTokens)
	a.state.delegationAudioOutputTokens += uint64(ev.DelegationAudioOutputTokens)
	a.state.delegationThinkingOutputTokens += uint64(ev.DelegationThinkingOutputTokens)
	a.state.audioMillis += ev.AudioMillis
	audioMillis := a.state.audioMillis
	a.stateMu.Unlock()

	if a.maxAudio > 0 && audioMillis >= uint64(a.maxAudio/time.Millisecond) {
		a.logger.WithField("audioMillis", audioMillis).Info("ai agent: audio budget reached")
		go a.Stop("budget")
	}
}
