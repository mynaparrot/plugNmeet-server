package insightsservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	return session.SendContextText("user", frame, true)
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

// pendingClientCall is one dispatched client tool call, bound to the participant
// it was sent to so no other participant can answer it.
type pendingClientCall struct {
	targetUserID string
	ch           chan *plugnmeet.AIAgentToolResultMsg
}

// HandleClientToolResult resolves the pending call only for the participant the call was dispatched to.
// It reports whether the answer was accepted.
func (a *AIAgent) HandleClientToolResult(userID string, res *plugnmeet.AIAgentToolResultMsg) bool {
	if res == nil || res.GetCallId() == "" {
		return false
	}

	a.pendingMu.Lock()
	entry, ok := a.pendingCalls[res.GetCallId()]
	if ok && entry.targetUserID != "" && userID != "" && entry.targetUserID != userID {
		a.pendingMu.Unlock()
		a.logger.WithFields(logrus.Fields{
			"callId":     res.GetCallId(),
			"userId":     userID,
			"targetUser": entry.targetUserID,
		}).Warn("ai agent: ignoring a client tool result from a participant that does not own the call")
		return false
	}
	delete(a.pendingCalls, res.GetCallId())
	a.pendingMu.Unlock()

	if !ok {
		a.logger.WithFields(logrus.Fields{"callId": res.GetCallId(), "ok": res.GetOk()}).Warn("ai agent: received late or unknown client tool result")
		return false
	}
	select {
	case entry.ch <- res:
	default:
	}

	return true
}

// CallClientTool implements mcp.ClientRPCSender: the call travels to the agent
// delegate's browser and its answer comes back as AI_AGENT_TOOL_RESULT.
func (a *AIAgent) CallClientTool(ctx context.Context, call mcp.ToolCall, timeout time.Duration) (mcp.ToolResult, error) {
	if a.transport == nil || a.delegate == nil {
		return mcp.ToolResult{}, errors.New("no client is available to run this tool. Speak your instruction instead")
	}
	target, err := a.delegate()
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("the participant client is not reachable: %w", err)
	}
	if target == "" {
		return mcp.ToolResult{}, errors.New("no client is available to run this tool. Speak your instruction instead")
	}

	callID := strings.TrimSpace(call.CallID)
	if callID == "" {
		callID = uuid.NewString()
	}
	call.CallID = callID

	ch := make(chan *plugnmeet.AIAgentToolResultMsg, 1)
	a.pendingMu.Lock()
	if _, exists := a.pendingCalls[callID]; exists {
		a.pendingMu.Unlock()
		return mcp.ToolResult{}, errors.New("another tool call is already in flight with the same id. Do not retry it")
	}
	a.pendingCalls[callID] = pendingClientCall{targetUserID: target, ch: ch}
	a.pendingMu.Unlock()
	defer func() {
		a.pendingMu.Lock()
		delete(a.pendingCalls, callID)
		a.pendingMu.Unlock()
	}()

	a.logger.WithFields(logrus.Fields{
		"callId":       call.CallID,
		"tool":         call.ToolName,
		"targetUserId": target,
		"timeout":      timeout.String(),
	}).Debug("ai agent: dispatching client tool")

	msg := &plugnmeet.AIAgentToolCallMsg{
		SessionId:      a.sessionId,
		CallId:         call.CallID,
		ToolName:       call.ToolName,
		ArgumentsJson:  call.ArgsJSON,
		TargetUserId:   target,
		DeadlineUnixMs: time.Now().Add(timeout).UnixMilli(),
		TraceId:        call.TraceID,
	}
	if err := a.transport.SendAIAgentToolCallToUser(a.roomId, target, msg); err != nil {
		return mcp.ToolResult{}, fmt.Errorf("the tool call could not be delivered: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case res := <-ch:
		if !res.GetOk() {
			reason := strings.TrimSpace(res.GetError())
			if reason == "" {
				reason = "the participant's browser could not run the tool"
			}
			return mcp.ToolResult{}, errors.New(reason)
		}
		out := strings.TrimSpace(res.GetResultJson())
		if out == "" {
			out = "{}"
		}
		return mcp.ToolResult{OutputJSON: out}, nil
	case <-ctx.Done():
		return mcp.ToolResult{}, ctx.Err()
	case <-timer.C:
		return mcp.ToolResult{}, context.DeadlineExceeded
	}
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
