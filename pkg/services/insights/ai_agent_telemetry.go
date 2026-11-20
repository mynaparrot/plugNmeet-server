package insightsservice

import (
	"context"
	"strings"
	"time"

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
	defer a.stateMu.Unlock()

	a.state.transcript = append(a.state.transcript, redisservice.AISessionTranscriptTurn{Role: role, Text: text})
	if excess := len(a.state.transcript) - maxSessionTranscriptTurns; excess > 0 {
		kept := make([]redisservice.AISessionTranscriptTurn, maxSessionTranscriptTurns)
		copy(kept, a.state.transcript[excess:])
		a.state.transcript = kept
	}
	if role == transcriptRoleParticipant {
		a.state.turnsCount++
	}
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
		DelegationInputTokens:  uint64(state.delegationInputTokens),
		DelegationOutputTokens: uint64(state.delegationOutputTokens),
		AudioMillis:            state.audioMillis,
		ToolsUsed:              toolsUsed,
		Transcript:             transcript,
	}
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
		"delegationInputTokens":  state.delegationInputTokens,
		"delegationOutputTokens": state.delegationOutputTokens,
		"audioMillis":            state.audioMillis,
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
	a.state.delegationInputTokens += ev.DelegationInputTokens
	a.state.delegationOutputTokens += ev.DelegationOutputTokens
	a.state.audioMillis += ev.AudioMillis
	audioMillis := a.state.audioMillis
	a.stateMu.Unlock()

	if a.maxAudio > 0 && audioMillis >= uint64(a.maxAudio/time.Millisecond) {
		a.logger.WithField("audioMillis", audioMillis).Info("ai agent: audio budget reached")
		go a.Stop("budget")
	}
}
