package insightsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
)

// aiAgentLogPayloadRunes bounds the tool arguments and output written to the debug log.
const aiAgentLogPayloadRunes = 300

// aiAgentAutoVisionInterval is the minimum gap between two automatic vision
// pushes, so a participant drawing quickly cannot flood the session.
const aiAgentAutoVisionInterval = 2 * time.Second

// allowAutoVision reports whether a whiteboard snapshot may be delivered. Only
// the automatic push of the participant's own edits is throttled; a page change,
// a notepad read and an explicit tool request always reach the session.
func (a *AIAgent) allowAutoVision(reason string) bool {
	if reason != "participant_edit" {
		return true
	}
	now := time.Now().UnixMilli()
	last := a.lastAutoVisionAt.Load()
	if last != 0 && now-last < aiAgentAutoVisionInterval.Milliseconds() {
		return false
	}

	return a.lastAutoVisionAt.CompareAndSwap(last, now)
}

// SendContext injects a server-side event that must not look like participant speech.
func (a *AIAgent) SendContext(text, source string) error {
	frame := systemContextFrame(text, source)
	if frame == "" {
		return nil
	}
	return a.sendContextText(frame)
}

// SendProactiveContext injects a server-side event and asks the provider to respond immediately.
func (a *AIAgent) SendProactiveContext(text, source string) error {
	frame := systemContextFrame(text, source)
	if frame == "" {
		return nil
	}
	session := a.currentSession()
	if session == nil {
		return errors.New("ai agent: no active session")
	}
	if err := session.SendContextText("user", frame, true); err != nil {
		return err
	}
	// The note is part of the record, but a poll answer or a timer is not a
	// participant turn, so it must not count as one.
	a.recordTranscriptTurn(transcriptRoleContext, frame)
	return nil
}

// systemContextFrame renders a sanitized [SYSTEM CONTEXT] block, or "" when
// there is nothing to send.
func systemContextFrame(text, source string) string {
	source, text = SanitizeSystemContextFrame(source, text)
	if text == "" {
		return ""
	}
	header := "[SYSTEM CONTEXT]"
	if source != "" {
		header = fmt.Sprintf("[SYSTEM CONTEXT — %s]", source)
	}
	return header + "\n" + text
}

// HandleWhiteboardScene hands a canvas snapshot to the provider, framed as
// untrusted data that also separates the agent's own elements from the
// participant's. A scene-graph-only snapshot is still delivered as text: the
// authorship and the text matter more than the pixels.
func (a *AIAgent) HandleWhiteboardScene(msg *plugnmeet.AIAgentWhiteboardSceneMsg) error {
	if msg == nil {
		return nil
	}
	hasImage := len(msg.GetWebpImage()) > 0
	hasSceneGraph := strings.TrimSpace(msg.GetSceneGraphJson()) != ""
	if !hasImage && !hasSceneGraph {
		return nil
	}
	if !a.allowAutoVision(msg.GetReason()) {
		return nil
	}
	session := a.currentSession()
	if session == nil {
		return errors.New("ai agent: no active session")
	}
	caption := WrapUntrustedCanvasContext(msg.GetReason(), msg.GetSceneGraphJson(), nil)
	if !hasImage {
		return session.SendContextText("user", caption, false)
	}
	if !hasSceneGraph {
		// An image is never delivered alone: without element metadata the agent
		// would have to guess ids and coordinates from pixels.
		caption += "\n\nNote: this snapshot arrived without element metadata, so element ids, text, and exact coordinates are unknown. Do not guess coordinates; call whiteboard_capture_snapshot with include_image=false first."
	}
	return session.SendImage("image/webp", msg.GetWebpImage(), caption)
}

// HandleParticipantChatMessage forwards one message a participant typed in the
// AI Chat box to the live session, framed as untrusted data, and lets the agent
// answer it straight away.
func (a *AIAgent) HandleParticipantChatMessage(userID, userName, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("ai agent: the chat message is empty")
	}
	select {
	case <-a.done:
		return errors.New("ai agent: the session has already stopped")
	default:
	}

	session := a.currentSession()
	if session == nil {
		return errors.New("ai agent: no active session")
	}

	if err := session.SendContextText("user", WrapUntrustedParticipantChatMessage(userID, userName, text), true); err != nil {
		return err
	}
	// A typed message is a participant turn too: keep it in the transcript and
	// count it, otherwise chat-only sessions would report zero turns.
	a.recordTranscriptTurn(transcriptRoleParticipant, text)
	return nil
}

func (a *AIAgent) handleToolCall(session insights.RealtimeSession, ev insights.RealtimeEvent) {
	call := mcp.ToolCall{
		CallID:      ev.CallID,
		RoomID:      a.roomId,
		SessionID:   a.sessionId,
		AgentUserID: a.agentUserId,
		ToolName:    ev.ToolName,
		ArgsJSON:    ev.ArgsJSON,
		TraceID:     ev.TraceID,
	}
	a.logger.WithFields(logrus.Fields{
		"callId":  ev.CallID,
		"tool":    ev.ToolName,
		"argsLen": len(ev.ArgsJSON),
		"args":    truncateRunes(ev.ArgsJSON, aiAgentLogPayloadRunes),
	}).Debug("ai agent: tool call received")
	a.recordToolUse(ev.ToolName)

	// The sentinel is inert and instant: run it inline so the refusal that
	// follows already carries the redirect hint.
	if ev.ToolName == toolNameReportOffTopic {
		a.sendToolResult(session, ev.CallID, ev.ToolName, a.executor.Execute(a.ctx, call))
		return
	}

	state := plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING
	if strings.HasPrefix(ev.ToolName, aiAgentWhiteboardToolPrefix) {
		state = plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING
	}
	a.setToolState(state, ev.ToolName)

	// Never block the event loop: audio and barge-in keep flowing while a tool runs.
	go func() {
		res := a.executor.Execute(a.ctx, call)
		fields := logrus.Fields{
			"callId":     ev.CallID,
			"tool":       ev.ToolName,
			"durationMs": res.Duration.Milliseconds(),
			"output":     truncateRunes(res.OutputJSON, aiAgentLogPayloadRunes),
		}
		if res.IsError {
			a.logger.WithFields(fields).Warn("ai agent: tool call failed")
		} else {
			a.logger.WithFields(fields).Debug("ai agent: tool call completed")
			// Only a completed call counts towards the mandatory activity quotas.
			if a.guard != nil {
				a.guard.RecordCompletedTool(ev.ToolName, ev.ArgsJSON)
			}
		}

		target := session
		if target == nil {
			target = a.currentSession()
		}
		a.sendToolResult(target, ev.CallID, ev.ToolName, res)
		a.restoreListening()
	}()
}

func (a *AIAgent) sendToolResult(session insights.RealtimeSession, callID, toolName string, res mcp.ToolResult) {
	if session == nil {
		a.logger.WithFields(logrus.Fields{"callId": callID, "tool": toolName}).Warn("ai agent: cannot send tool result, no active session")
		return
	}
	if err := session.SendToolResult(callID, toolName, res.OutputJSON, res.IsError); err != nil {
		a.logger.WithError(err).WithFields(logrus.Fields{"callId": callID, "tool": toolName}).Warn("ai agent: failed to send the tool result")
	}
}

// reportOffTopicTool declares the silent sentinel tool. The event loop already
// routes report_off_topic_attempt calls here; the handler keeps the behaviour
// identical if a call ever reaches the executor directly.
func reportOffTopicTool(guard *BoundaryGuard) *mcp.Tool {
	return &mcp.Tool{
		Kind:      mcp.ToolKindServer,
		Essential: true,
		Spec:      ReportOffTopicToolSpec(),
		Handler: func(_ context.Context, call mcp.ToolCall) (mcp.ToolResult, error) {
			var in struct {
				Category string `json:"category"`
				Summary  string `json:"user_request_summary"`
			}
			_ = json.Unmarshal([]byte(call.ArgsJSON), &in)

			strikes, hint, _ := guard.RecordOffTopicAttempt(in.Category, in.Summary)
			out, err := json.Marshal(map[string]any{
				"recorded":      true,
				"strikes":       strikes,
				"redirect_hint": hint,
			})
			if err != nil {
				return mcp.ToolResult{}, err
			}
			return mcp.ToolResult{OutputJSON: string(out)}, nil
		},
	}
}
