package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/openai/openai-go/v3/live"
)

// testLiveDelegationModel stands in for the configured delegation_model.
const testLiveDelegationModel = "gpt-5.4-mini"

// fakeLiveConn records writes and replays scripted server events.
type fakeLiveConn struct {
	mu        sync.Mutex
	incoming  []live.ServerEventUnion
	sent      []live.ClientEventUnionParam
	block     chan struct{}
	closed    bool
	writeFail error
}

func (c *fakeLiveConn) Send(_ context.Context, event live.ClientEventUnionParam) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeFail != nil {
		return c.writeFail
	}
	c.sent = append(c.sent, event)
	return nil
}

func (c *fakeLiveConn) Recv(_ context.Context) (live.ServerEventUnion, error) {
	c.mu.Lock()
	if len(c.incoming) == 0 {
		block := c.block
		c.mu.Unlock()
		if block != nil {
			<-block
		}
		return live.ServerEventUnion{}, io.EOF
	}
	msg := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	return msg, nil
}

func (c *fakeLiveConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.block != nil {
		close(c.block)
		c.block = nil
	}
	return nil
}

func (c *fakeLiveConn) sentEvents() []live.ClientEventUnionParam {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := make([]live.ClientEventUnionParam, len(c.sent))
	copy(events, c.sent)
	return events
}

func drainRealtimeEvents(t *testing.T, events <-chan insights.RealtimeEvent) []insights.RealtimeEvent {
	t.Helper()

	var drained []insights.RealtimeEvent
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return drained
			}
			drained = append(drained, event)
		default:
			return drained
		}
	}
}

func nextRealtimeEvent(t *testing.T, events <-chan insights.RealtimeEvent) insights.RealtimeEvent {
	t.Helper()

	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event channel closed unexpectedly")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an event")
	}
	return insights.RealtimeEvent{}
}

func TestBuildLiveSessionConfig(t *testing.T) {
	opts := &insights.RealtimeSessionOptions{
		SystemInstruction: "You are a tutor.",
		Tools: []*insights.MCPToolSpec{
			{Name: "whiteboard_write_content", Description: "Write on the board", InputSchema: map[string]any{"type": "object"}},
			nil,
			{Name: ""},
		},
	}

	cfg := buildLiveSessionConfig(defaultLiveModel, testLiveDelegationModel, "marin", opts)

	if cfg.Model != live.SessionConfigModelGPTLive1 {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Instructions.Or("") != "You are a tutor." {
		t.Fatalf("instructions not mapped: %v", cfg.Instructions)
	}
	if cfg.Audio.Format.OfAudioPCM == nil || cfg.Audio.Format.OfAudioPCM.Rate != liveAudioSampleRate {
		t.Fatalf("unexpected audio format: %+v", cfg.Audio.Format)
	}
	if cfg.Audio.Output.Voice.OfString.Or("") != "marin" {
		t.Fatalf("unexpected voice: %v", cfg.Audio.Output.Voice.OfString)
	}

	delegation := cfg.Delegation.OfResponses
	if delegation == nil {
		t.Fatal("responses delegation not configured")
	}
	if delegation.Responses.Model != testLiveDelegationModel {
		t.Fatalf("unexpected delegation model: %s", delegation.Responses.Model)
	}
	if len(delegation.Responses.Tools) != 1 || delegation.Responses.Tools[0].OfFunction == nil {
		t.Fatalf("delegation tools not mapped: %+v", delegation.Responses.Tools)
	}
	function := delegation.Responses.Tools[0].OfFunction
	if function.Name != "whiteboard_write_content" || function.Description.Or("") != "Write on the board" {
		t.Fatalf("unexpected function tool: %+v", function)
	}
}

func TestBuildLiveSessionConfigMinimal(t *testing.T) {
	cfg := buildLiveSessionConfig(defaultLiveModel, "", "", &insights.RealtimeSessionOptions{})

	if cfg.Instructions.Valid() {
		t.Fatal("empty options must not set instructions")
	}
	if cfg.Audio.Output.Voice.OfString.Valid() {
		t.Fatal("empty voice must not be set")
	}
	if cfg.Delegation.OfResponses != nil {
		t.Fatalf("delegation must stay off without a model or tools: %+v", cfg.Delegation)
	}
}

func TestStartLiveSessionHandshake(t *testing.T) {
	conn := &fakeLiveConn{incoming: []live.ServerEventUnion{
		// Arrives before session.started and must be replayed afterwards.
		{Type: liveEventInputTranscript, OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: "hello"}},
		{Type: liveEventSessionStarted, OfSessionStartedEvent: live.SessionStartedEvent{EventID: "evt_1"}},
	}}

	session, err := startLiveSession(context.Background(), conn, live.SessionConfigParam{}, nil, 0)
	if err != nil {
		t.Fatalf("startLiveSession: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfSessionStart == nil {
		t.Fatalf("session.start not sent: %+v", events)
	}

	event := nextRealtimeEvent(t, session.Events())
	if event.Type != insights.RealtimeEventTranscriptUser || event.Text != "hello" {
		t.Fatalf("buffered event was not replayed: %+v", event)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStartLiveSessionFailsOnErrorBeforeStart(t *testing.T) {
	conn := &fakeLiveConn{incoming: []live.ServerEventUnion{
		{Type: liveEventError, OfErrorEvent: live.ErrorEvent{Error: live.LiveError{Message: "bad config", Code: "invalid_request"}}},
	}}

	session, err := startLiveSession(context.Background(), conn, live.SessionConfigParam{}, nil, 0)
	if err == nil {
		t.Fatal("an error before session.started must fail the start")
	}
	if session != nil {
		t.Fatal("no session must be returned on failure")
	}
	if !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !conn.closed {
		t.Fatal("a failed handshake must close the socket")
	}
}

func TestStartLiveSessionFailsWhenClosedBeforeStart(t *testing.T) {
	conn := &fakeLiveConn{incoming: []live.ServerEventUnion{
		{Type: liveEventSessionClosed, OfSessionClosedEvent: live.SessionClosedEvent{Reason: "content"}},
	}}

	if _, err := startLiveSession(context.Background(), conn, live.SessionConfigParam{}, nil, 0); err == nil {
		t.Fatal("a session.closed before session.started must fail the start")
	}
	if !conn.closed {
		t.Fatal("a failed handshake must close the socket")
	}
}

func TestStartLiveSessionSendFailure(t *testing.T) {
	conn := &fakeLiveConn{writeFail: errors.New("socket is broken")}

	if _, err := startLiveSession(context.Background(), conn, live.SessionConfigParam{}, nil, 0); err == nil {
		t.Fatal("a failed session.start must be reported")
	}
	if !conn.closed {
		t.Fatal("a failed session.start must close the socket")
	}
}

func TestLiveSessionSendAudio(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.SendAudio(make([]byte, 1600)); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfSessionInputAudioAppend == nil {
		t.Fatalf("audio not forwarded: %+v", events)
	}
	pcm, decodeErr := base64.StdEncoding.DecodeString(events[0].OfSessionInputAudioAppend.Audio)
	if decodeErr != nil {
		t.Fatalf("audio is not base64: %v", decodeErr)
	}
	if len(pcm) != 2400 {
		t.Fatalf("expected 2400 resampled bytes, got %d", len(pcm))
	}

	if err = s.SendAudio(nil); err != nil {
		t.Fatalf("empty audio must be a no-op: %v", err)
	}
	if events = conn.sentEvents(); len(events) != 1 {
		t.Fatalf("empty audio must not be forwarded: %+v", events)
	}
}

func TestLiveSessionSendContextText(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.SendContextText("user", "the poll is closed", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err = s.SendContextText("system", "stay on topic", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err = s.SendContextText("user", "read the poll results", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 3 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if events[0].OfSessionThinkingAppend == nil || events[0].OfSessionThinkingAppend.Content != "the poll is closed" {
		t.Fatalf("private context must be thinking: %+v", events[0])
	}
	if events[1].OfSessionInstructionsAppend == nil || events[1].OfSessionInstructionsAppend.Content != "stay on topic" {
		t.Fatalf("system context must be instructions: %+v", events[1])
	}
	if events[2].OfSessionCommentaryAppend == nil || events[2].OfSessionCommentaryAppend.Content != "read the poll results" {
		t.Fatalf("triggered context must be commentary: %+v", events[2])
	}

	if err = s.SendContextText("user", "", true); err != nil {
		t.Fatalf("empty context must be a no-op: %v", err)
	}
	if events = conn.sentEvents(); len(events) != 3 {
		t.Fatalf("empty context text must not be forwarded: %+v", events)
	}
}

func TestLiveSessionSendImage(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.SendImage("", []byte("img"), "look at the board"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfResponseItemCreate == nil {
		t.Fatalf("image not forwarded: %+v", events)
	}
	message := events[0].OfResponseItemCreate.Item.OfInputMessage
	if message == nil {
		t.Fatalf("image must be an input message: %+v", events[0].OfResponseItemCreate.Item)
	}
	if message.Role != "user" || message.Type != "message" {
		t.Fatalf("unexpected message: %+v", message)
	}
	if len(message.Content) != 2 {
		t.Fatalf("expected image plus caption: %+v", message.Content)
	}
	image := message.Content[0].OfInputImage
	if image == nil || !strings.HasPrefix(image.ImageURL.Or(""), "data:image/webp;base64,") {
		t.Fatalf("unexpected image part: %+v", message.Content[0])
	}
	if caption := message.Content[1].OfInputText; caption == nil || caption.Text != "look at the board" {
		t.Fatalf("unexpected caption: %+v", message.Content[1])
	}

	if err = s.SendImage("image/png", nil, "ignored"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	if events = conn.sentEvents(); len(events) != 1 {
		t.Fatalf("empty image must not be forwarded: %+v", events)
	}
}

func TestLiveSessionSendToolResult(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.SendToolResult("call-1", "get_poll_results", `{"votes":3}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 2 {
		t.Fatalf("tool result must be followed by a response.create: %+v", events)
	}
	output := events[0].OfResponseItemCreate.Item.OfFunctionCallOutput
	if output == nil || output.CallID.Or("") != "call-1" {
		t.Fatalf("unexpected function call output: %+v", events[0].OfResponseItemCreate.Item)
	}
	if output.Output.OfString.Or("") != `{"votes":3}` {
		t.Fatalf("unexpected output payload: %+v", output.Output)
	}
	if events[1].OfResponseCreate == nil {
		t.Fatalf("response.create not sent: %+v", events[1])
	}

	if err = s.SendToolResult("call-2", "get_poll_results", "", true); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	events = conn.sentEvents()
	if failed := events[2].OfResponseItemCreate.Item.OfFunctionCallOutput; !strings.Contains(failed.Output.OfString.Or(""), `"error"`) {
		t.Fatalf("failed tool results must be wrapped: %+v", failed.Output)
	}

	if err = s.SendToolResult("", "get_poll_results", "{}", false); err == nil {
		t.Fatal("a tool result without a call id must fail")
	}
}

func TestLiveSessionClose(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfSessionClose == nil {
		t.Fatalf("session.close not sent: %+v", events)
	}
	if !conn.closed {
		t.Fatal("Close must close the socket")
	}
	if err = s.SendAudio([]byte{1, 2}); !errors.Is(err, errLiveSessionClosed) {
		t.Fatalf("expected errLiveSessionClosed, got %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if err = s.Interrupt(); err != nil {
		t.Fatalf("Interrupt must be a no-op: %v", err)
	}
}

func TestLiveSessionSendError(t *testing.T) {
	conn := &fakeLiveConn{writeFail: errors.New("socket is broken")}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if err = s.SendAudio([]byte{1, 2}); err == nil {
		t.Fatal("send errors must be reported")
	}
}

func TestLiveSessionServerAudioAndBargeIn(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	pcm := []byte{1, 2, 3, 4}
	s.handleServerEvent(live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString(pcm)},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].Type != insights.RealtimeEventAudioDelta || string(events[0].AudioPCM24k) != string(pcm) {
		t.Fatalf("unexpected audio events: %+v", events)
	}

	// Participant speech while the agent talked is a barge-in.
	s.handleServerEvent(live.ServerEventUnion{
		Type:                        liveEventInputTranscript,
		OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: "wait"},
	})

	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("expected barge-in plus fallback transcript: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventInterrupted {
		t.Fatalf("expected interrupted first: %+v", events)
	}
	if events[1].Type != insights.RealtimeEventTranscriptUser || events[1].Text != "wait" {
		t.Fatalf("ungroupable deltas must fall back to raw transcripts: %+v", events)
	}

	// A second input delta must not report the barge-in again.
	s.handleServerEvent(live.ServerEventUnion{
		Type:                        liveEventInputTranscript,
		OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: "a moment"},
	})
	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTranscriptUser {
		t.Fatalf("barge-in must be reported once: %+v", events)
	}
}

func TestLiveSessionServerTranscriptGrouping(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                        liveEventInputTranscript,
		OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{EventID: "evt_1", Delta: "hello", StartMs: 0, EndMs: 400},
	})
	s.handleServerEvent(live.ServerEventUnion{
		Type:                         liveEventOutputTranscript,
		OfOutputTranscriptDeltaEvent: live.OutputTranscriptDeltaEvent{EventID: "evt_2", Delta: "hi there", StartMs: 1000, EndMs: 1400},
	})
	s.grouper.Close()

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("expected one update per speaker: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventTranscriptUser || events[0].Text != "hello" {
		t.Fatalf("unexpected user transcript: %+v", events[0])
	}
	if events[1].Type != insights.RealtimeEventTranscriptAgent || events[1].Text != "hi there" {
		t.Fatalf("unexpected agent transcript: %+v", events[1])
	}
}

func TestLiveSessionTranscriptUpdates(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "user", Text: "hello"}, Reason: "speaker_change"})
	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "assistant", Text: "hi there"}})
	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "system", Text: "ignored"}})
	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "assistant", Text: "hi there."}, Reason: "inactivity"})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 4 {
		t.Fatalf("unexpected updates: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventTranscriptUser || !events[0].Final {
		t.Fatalf("user updates must be final on a speaker change: %+v", events[0])
	}
	if events[1].Type != insights.RealtimeEventTranscriptAgent || events[1].Final {
		t.Fatalf("display updates must not be final: %+v", events[1])
	}
	if events[2].Type != insights.RealtimeEventTranscriptAgent || !events[2].Final {
		t.Fatalf("unexpected agent transcript: %+v", events[2])
	}
	if events[3].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("a final agent transcript must complete the turn: %+v", events[3])
	}
	if s.setSpeaking(false) {
		t.Fatal("a final agent transcript must reset the speaking flag")
	}
}

func TestLiveSessionServerResponseEvents(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseOutputItemDone,
			"item": map[string]any{
				"type":      liveItemTypeFunctionCall,
				"call_id":   "call-1",
				"name":      "get_poll_results",
				"arguments": `{"poll_id":"1"}`,
			},
		}},
	})
	s.handleServerEvent(live.ServerEventUnion{
		Type:            liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{"type": liveResponseCompleted}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 1 {
		t.Fatalf("a function call turn must not complete the turn: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventToolCall || events[0].CallID != "call-1" || events[0].ToolName != "get_poll_results" {
		t.Fatalf("unexpected tool call: %+v", events[0])
	}
	if events[0].ArgsJSON != `{"poll_id":"1"}` {
		t.Fatalf("unexpected tool args: %q", events[0].ArgsJSON)
	}

	// The next response has no function call, so the turn completes.
	s.handleServerEvent(live.ServerEventUnion{
		Type:            liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{"type": liveResponseCompleted}},
	})
	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("expected turn complete: %+v", events)
	}

	// An item without an id still yields a tool call with default arguments.
	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseOutputItemDone,
			"item": map[string]any{"type": liveItemTypeFunctionCall, "id": "call-2", "name": "create_quiz_or_poll"},
		}},
	})
	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].CallID != "call-2" || events[0].ArgsJSON != "{}" {
		t.Fatalf("expected a default argument tool call: %+v", events)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type":     liveResponseFailed,
			"response": map[string]any{"error": map[string]any{"message": "backend exploded"}},
		}},
	})
	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].Type != insights.RealtimeEventError || !strings.Contains(events[0].Err.Error(), "backend exploded") {
		t.Fatalf("unexpected failure event: %+v", events)
	}
}

func TestLiveSessionServerDelegationUsage(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseCompleted,
			"response": map[string]any{
				"usage": map[string]any{"input_tokens": float64(120), "output_tokens": int64(40)},
			},
		}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("expected a usage event and a turn complete: %+v", events)
	}
	usage := events[0]
	if usage.Type != insights.RealtimeEventUsage || usage.DelegationInputTokens != 120 || usage.DelegationOutputTokens != 40 {
		t.Fatalf("unexpected usage event: %+v", usage)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("delegated tokens must not be reported as session tokens: %+v", usage)
	}
	if events[1].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("the usage event must precede the turn complete: %+v", events[1])
	}

	// A response without usage must not report one.
	s.handleServerEvent(live.ServerEventUnion{
		Type:            liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{"type": liveResponseCompleted}},
	})
	if events = drainRealtimeEvents(t, s.Events()); len(events) != 1 || events[0].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("expected only a turn complete: %+v", events)
	}
}

func TestNestedUint32(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  uint32
	}{
		{"missing", nil, 0},
		{"float64", float64(42), 42},
		{"int64", int64(42), 42},
		{"int", 42, 42},
		{"uint32", uint32(42), 42},
		{"uint64", uint64(42), 42},
		{"json number", json.Number("42"), 42},
		{"negative float", float64(-3), 0},
		{"negative int", int64(-3), 0},
		{"oversized", int64(math.MaxUint32) + 10, math.MaxUint32},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values := map[string]any{}
			if tt.value != nil {
				values["key"] = tt.value
			}
			if got := nestedUint32(values, "key"); got != tt.want {
				t.Fatalf("nestedUint32 = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCreateLiveSessionRequiresDelegationModel(t *testing.T) {
	provider := &OpenAIProvider{}

	if _, err := provider.createLiveSession(context.Background(), "gpt-live-1", &insights.RealtimeSessionOptions{}); err == nil || !strings.Contains(err.Error(), "delegation_model") {
		t.Fatalf("expected a delegation_model error, got %v", err)
	}
}

func TestLiveSessionServerUsage(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                       liveEventSessionUsageUpdate,
		OfSessionUsageUpdatedEvent: live.SessionUsageUpdatedEvent{Usage: live.SessionUsage{Seconds: 1.5}},
	})
	s.handleServerEvent(live.ServerEventUnion{
		Type:                       liveEventSessionUsageUpdate,
		OfSessionUsageUpdatedEvent: live.SessionUsageUpdatedEvent{Usage: live.SessionUsage{Seconds: 2}},
	})
	s.handleServerEvent(live.ServerEventUnion{
		Type:                       liveEventSessionUsageUpdate,
		OfSessionUsageUpdatedEvent: live.SessionUsageUpdatedEvent{Usage: live.SessionUsage{Seconds: 2}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("duplicate usage must not be reported: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventUsage || events[0].AudioMillis != 1500 {
		t.Fatalf("unexpected first usage: %+v", events[0])
	}
	if events[1].Type != insights.RealtimeEventUsage || events[1].AudioMillis != 500 {
		t.Fatalf("usage must be incremental: %+v", events[1])
	}
}

func TestLiveSessionServerClosed(t *testing.T) {
	tests := []struct {
		reason string
		want   insights.RealtimeEventType
	}{
		{reason: "expired", want: insights.RealtimeEventGoAway},
		{reason: "close_requested"},
		{reason: "", want: ""},
		{reason: "content", want: insights.RealtimeEventError},
	}

	for _, tc := range tests {
		t.Run(tc.reason, func(t *testing.T) {
			conn := &fakeLiveConn{}
			s, err := newLiveSession(conn, nil)
			if err != nil {
				t.Fatalf("newLiveSession: %v", err)
			}

			s.handleServerEvent(live.ServerEventUnion{
				Type:                 liveEventSessionClosed,
				OfSessionClosedEvent: live.SessionClosedEvent{Reason: live.SessionClosedEventReasonString(tc.reason)},
			})

			events := drainRealtimeEvents(t, s.Events())
			switch tc.want {
			case "":
				if len(events) != 0 {
					t.Fatalf("a requested close must stay silent: %+v", events)
				}
			default:
				if len(events) != 1 || events[0].Type != tc.want {
					t.Fatalf("unexpected close event: %+v", events)
				}
				if tc.want == insights.RealtimeEventGoAway && events[0].Text != "session expired" {
					t.Fatalf("unexpected go_away text: %q", events[0].Text)
				}
			}
		})
	}
}

func TestLiveSessionServerError(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:         liveEventError,
		OfErrorEvent: live.ErrorEvent{Error: live.LiveError{Message: "invalid command", Code: "invalid_request"}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 1 || events[0].Type != insights.RealtimeEventError {
		t.Fatalf("unexpected error event: %+v", events)
	}
	if !strings.Contains(events[0].Err.Error(), "invalid command") || !strings.Contains(events[0].Err.Error(), "invalid_request") {
		t.Fatalf("unexpected error message: %v", events[0].Err)
	}
}

func TestLiveSessionReadLoop(t *testing.T) {
	conn := &fakeLiveConn{incoming: []live.ServerEventUnion{
		{Type: liveEventOutputAudioDelta, OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{
			Delta: base64.StdEncoding.EncodeToString([]byte{9, 9}),
		}},
	}}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}
	s.wg.Add(1)
	go s.readLoop()

	var events []insights.RealtimeEvent
	for event := range s.Events() {
		events = append(events, event)
	}

	if len(events) != 2 {
		t.Fatalf("expected audio plus read error, got %+v", events)
	}
	if events[0].Type != insights.RealtimeEventAudioDelta || len(events[0].AudioPCM24k) != 2 {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != insights.RealtimeEventError || events[1].Err == nil {
		t.Fatalf("a read error must be reported: %+v", events[1])
	}

	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLiveSessionMaxDurationEmitsGoAway(t *testing.T) {
	conn := &fakeLiveConn{block: make(chan struct{})}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}
	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(context.Background(), 5*time.Millisecond)

	select {
	case event := <-s.Events():
		if event.Type != insights.RealtimeEventGoAway {
			t.Fatalf("expected go_away, got %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the go_away event")
	}

	for range s.Events() {
	}

	if !conn.closed {
		t.Fatal("max duration must close the socket")
	}
}
