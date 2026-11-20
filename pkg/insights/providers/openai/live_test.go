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
	"unicode/utf8"

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

// The API rejects an empty delegation_id: general session context must send
// null instead of "".
func TestLiveSessionContextTextSendsNullDelegationID(t *testing.T) {
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
	for _, event := range events {
		payload, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if strings.Contains(string(payload), `"delegation_id":""`) {
			t.Fatalf("empty delegation_id is rejected by the API: %s", payload)
		}
		if !strings.Contains(string(payload), `"delegation_id":null`) {
			t.Fatalf("general session context requires a null delegation_id: %s", payload)
		}
	}
}

// A whiteboard scene graph is far above the Live API's 500 token append limit:
// the append must stay bounded while the delegated Responses model still receives
// the full text.
func TestLiveSessionSendContextTextBoundsLongAppend(t *testing.T) {
	scene := `{"type":"excalidraw","elements":[` + strings.Repeat(`{"id":"el","type":"text","x":120,"y":80,"text":"x=42"},`, 200) + `]}`
	if runes := len([]rune(scene)); runes <= liveAppendMaxRunes {
		t.Fatalf("the test payload must exceed the append budget: %d runes", runes)
	}

	cases := []struct {
		name             string
		role             string
		trigger          bool
		wantCommentary   bool
		wantInstructions bool
	}{
		{name: "thinking", role: "user"},
		{name: "instructions", role: "system", wantInstructions: true},
		{name: "commentary", role: "user", trigger: true, wantCommentary: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeLiveConn{}
			s, err := newLiveSession(conn, nil)
			if err != nil {
				t.Fatalf("newLiveSession: %v", err)
			}

			if err = s.SendContextText(tc.role, scene, tc.trigger); err != nil {
				t.Fatalf("SendContextText: %v", err)
			}

			events := conn.sentEvents()
			if len(events) != 2 {
				t.Fatalf("long context must append an item plus one bounded append: %+v", events)
			}

			item := events[0].OfResponseItemCreate
			if item == nil || item.Item.OfInputMessage == nil {
				t.Fatalf("the delegated model must receive the full text first: %+v", events[0])
			}
			message := item.Item.OfInputMessage
			if message.Role != "user" || len(message.Content) != 1 {
				t.Fatalf("unexpected context item: %+v", message)
			}
			if text := message.Content[0].OfInputText; text == nil || text.Text != scene {
				t.Fatalf("the delegated model must receive the untruncated text")
			}

			var content string
			switch {
			case events[1].OfSessionThinkingAppend != nil:
				content = events[1].OfSessionThinkingAppend.Content
			case events[1].OfSessionInstructionsAppend != nil:
				content = events[1].OfSessionInstructionsAppend.Content
			case events[1].OfSessionCommentaryAppend != nil:
				content = events[1].OfSessionCommentaryAppend.Content
			}

			if content == "" {
				t.Fatalf("missing session append: %+v", events[1])
			}
			if tc.wantCommentary != (events[1].OfSessionCommentaryAppend != nil) {
				t.Fatalf("triggered context must be commentary: %+v", events[1])
			}
			if tc.wantInstructions != (events[1].OfSessionInstructionsAppend != nil) {
				t.Fatalf("system context must be instructions: %+v", events[1])
			}
			// One rune can cost a whole token, so runes are the budget.
			if runes := len([]rune(content)); runes > liveAppendMaxRunes {
				t.Fatalf("append content of %d runes exceeds the budget: %q", runes, content)
			}
			if liveAppendMaxRunes >= 500 {
				t.Fatalf("the append budget must stay below the 500 token limit")
			}
			if !strings.HasSuffix(content, liveTruncationNote) {
				t.Fatalf("a cut append must say so: %q", content)
			}
		})
	}
}

// Short context keeps the old shape: one append, no duplicated conversation item.
func TestLiveSessionSendContextTextDoesNotDuplicateShortText(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	text := strings.Repeat("a", liveAppendMaxRunes)
	if err = s.SendContextText("user", text, false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfSessionThinkingAppend == nil {
		t.Fatalf("text at the budget must not be routed twice: %+v", events)
	}
	if events[0].OfSessionThinkingAppend.Content != text {
		t.Fatalf("unexpected append content: %q", events[0].OfSessionThinkingAppend.Content)
	}
}

func TestBoundLiveAppend(t *testing.T) {
	short := "the poll is closed"
	if got, cut := boundLiveAppend(short, liveAppendMaxRunes); cut || got != short {
		t.Fatalf("short text must pass through: %q %v", got, cut)
	}

	// Multi-byte runes must never be split.
	long := strings.Repeat("计算", 1000)
	got, cut := boundLiveAppend(long, liveAppendMaxRunes)
	if !cut {
		t.Fatalf("long text must be cut")
	}
	if runes := len([]rune(got)); runes > liveAppendMaxRunes {
		t.Fatalf("cut text of %d runes exceeds the budget", runes)
	}
	if !utf8.ValidString(got) || !strings.HasSuffix(got, liveTruncationNote) {
		t.Fatalf("unexpected cut text: %q", got)
	}

	// A budget below the note length still yields valid text instead of panicking.
	got, cut = boundLiveAppend("abcdefgh", 4)
	if !cut || !utf8.ValidString(got) {
		t.Fatalf("unexpected tiny budget result: %q %v", got, cut)
	}
}

func TestLiveSessionAudioIdleCompletesTurn(t *testing.T) {
	restoreLiveSpeakingIdle(t, 20*time.Millisecond)

	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString([]byte{1, 2})},
	})

	events := []insights.RealtimeEvent{nextRealtimeEvent(t, s.Events())}
	if events[0].Type != insights.RealtimeEventAudioDelta {
		t.Fatalf("expected audio first: %+v", events[0])
	}
	events = append(events, nextRealtimeEvent(t, s.Events()))
	if events[1].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("a silent audio stream must complete the turn: %+v", events[1])
	}
	if s.isSpeaking() {
		t.Fatal("an idle audio stream must reset the speaking flag")
	}
}

// A trailing chunk after the turn was already completed must not leave the
// session speaking forever: it opens a new turn that completes on its own.
func TestLiveSessionTrailingAudioAfterTurnComplete(t *testing.T) {
	restoreLiveSpeakingIdle(t, 20*time.Millisecond)

	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString([]byte{1, 2})},
	})
	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "assistant", Text: "hello there"}, Reason: "inactivity"})
	s.handleServerEvent(live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString([]byte{3, 4})},
	})

	var completions int
	deadline := time.After(2 * time.Second)
	for completions < 2 {
		select {
		case event := <-s.Events():
			if event.Type == insights.RealtimeEventTurnComplete {
				completions++
			}
		case <-deadline:
			t.Fatalf("expected one turn completion per speaking turn, got %d", completions)
		}
	}
	if s.isSpeaking() {
		t.Fatal("the trailing chunk must not leave the session speaking")
	}
}

func restoreLiveSpeakingIdle(t *testing.T, idle time.Duration) {
	t.Helper()
	previous := liveSpeakingIdleTimeout
	liveSpeakingIdleTimeout = idle
	t.Cleanup(func() { liveSpeakingIdleTimeout = previous })
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

// liveToolCallEvents feeds one response that calls the given tools and returns
// the tool call events the session published.
func liveToolCallEvents(t *testing.T, s *liveSession, calls ...[2]string) []insights.RealtimeEvent {
	t.Helper()

	output := make([]any, 0, len(calls))
	for _, call := range calls {
		item := map[string]any{
			"type":      liveItemTypeFunctionCall,
			"call_id":   call[0],
			"name":      call[1],
			"arguments": `{"a":1}`,
		}
		output = append(output, item)
		s.handleServerEvent(live.ServerEventUnion{
			Type:            liveEventResponse,
			OfResponseEvent: live.ResponseEvent{Event: map[string]any{"type": liveResponseOutputItemDone, "item": item}},
		})
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type":     liveResponseCompleted,
			"response": map[string]any{"output": output},
		}},
	})

	return drainRealtimeEvents(t, s.Events())
}

// A response may call several tools at once: response.create must wait until the
// last function_call_output is on the wire, and any context that arrives in
// between must not jump the queue.
func TestLiveSessionMultipleToolCallsWaitForAllOutputs(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	events := liveToolCallEvents(t, s,
		[2]string{"call_1", "whiteboard_manage_board"},
		[2]string{"call_2", "whiteboard_write_content"},
	)
	if len(events) != 2 {
		t.Fatalf("both tool calls of the response must be released together: %+v", events)
	}
	if events[0].CallID != "call_1" || events[1].CallID != "call_2" {
		t.Fatalf("unexpected tool calls: %+v", events)
	}

	// The first tool finished while the second one still runs.
	if err = s.SendToolResult("call_1", "whiteboard_manage_board", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	sent := conn.sentEvents()
	if len(sent) != 1 {
		t.Fatalf("an unanswered sibling call must withhold response.create: %+v", sent)
	}
	if callID := sent[0].OfResponseItemCreate.Item.OfFunctionCallOutput.CallID.Or(""); callID != "call_1" {
		t.Fatalf("unexpected function call output: %+v", sent[0])
	}

	// Whiteboard context that arrives while the second tool runs waits too.
	if err = s.SendContextText("user", "the board now shows x=42", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err = s.SendImage("image/webp", []byte("img"), "the board"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	if sent = conn.sentEvents(); len(sent) != 1 {
		t.Fatalf("context must wait for the pending tool output: %+v", sent)
	}

	if err = s.SendToolResult("call_2", "whiteboard_write_content", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	sent = conn.sentEvents()
	if len(sent) != 5 {
		t.Fatalf("expected the second output, both deferred events and response.create: %+v", sent)
	}
	if callID := sent[1].OfResponseItemCreate.Item.OfFunctionCallOutput.CallID.Or(""); callID != "call_2" {
		t.Fatalf("unexpected second function call output: %+v", sent[1])
	}
	if sent[2].OfSessionThinkingAppend == nil || sent[2].OfSessionThinkingAppend.Content != "the board now shows x=42" {
		t.Fatalf("deferred context must follow the last output: %+v", sent[2])
	}
	if sent[3].OfResponseItemCreate.Item.OfInputMessage == nil {
		t.Fatalf("the deferred image must follow the context: %+v", sent[3])
	}
	if sent[4].OfResponseCreate == nil {
		t.Fatalf("response.create must stay last: %+v", sent[4])
	}
}

// A response that reports its calls only in response.completed must still be
// registered, so the tool result cannot resume the conversation too early.
func TestLiveSessionToolCallsFromCompletedResponse(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseCompleted,
			"response": map[string]any{"output": []any{
				map[string]any{"type": "message"},
				map[string]any{"type": liveItemTypeFunctionCall, "call_id": "call_9", "name": "whiteboard_capture_snapshot", "arguments": `{"include_image":false}`},
			}},
		}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 1 {
		t.Fatalf("a completed response must release its tool calls: %+v", events)
	}
	if events[0].Type != insights.RealtimeEventToolCall || events[0].CallID != "call_9" {
		t.Fatalf("unexpected tool call: %+v", events[0])
	}

	if err = s.SendToolResult("call_9", "whiteboard_capture_snapshot", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	if sent := conn.sentEvents(); len(sent) != 2 || sent[1].OfResponseCreate == nil {
		t.Fatalf("the only pending call must resume the conversation: %+v", sent)
	}
}

// The last function_call_output releases the context held back during the turn.
func TestLiveSessionDeferredContextFollowsToolOutput(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if events := liveToolCallEvents(t, s, [2]string{"call_1", "set_workspace_lock"}); len(events) != 1 {
		t.Fatalf("unexpected tool calls: %+v", events)
	}
	if err = s.SendContextText("user", "the room is locked", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err = s.SendToolResult("call_1", "set_workspace_lock", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}

	sent := conn.sentEvents()
	if len(sent) != 3 {
		t.Fatalf("expected the output, the deferred context and response.create: %+v", sent)
	}
	if sent[1].OfSessionThinkingAppend == nil || sent[2].OfResponseCreate == nil {
		t.Fatalf("unexpected order: %+v", sent)
	}
}

// A deferred commentary append triggers the response on its own, so the tool
// result must not send a second response.create on top of it.
func TestLiveSessionDeferredCommentaryTriggersResponseOnce(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if events := liveToolCallEvents(t, s, [2]string{"call_1", "whiteboard_capture_snapshot"}); len(events) != 1 {
		t.Fatalf("unexpected tool calls: %+v", events)
	}
	if err = s.SendContextText("user", "read the board out loud", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err = s.SendToolResult("call_1", "whiteboard_capture_snapshot", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}

	sent := conn.sentEvents()
	if len(sent) != 2 {
		t.Fatalf("expected the output and the deferred commentary only: %+v", sent)
	}
	if sent[1].OfSessionCommentaryAppend == nil {
		t.Fatalf("the deferred commentary must follow the output: %+v", sent[1])
	}
}

// A failed response abandons its tool turn, so a later tool result must not be
// blocked by calls that can never be answered.
func TestLiveSessionFailedResponseResetsToolTurn(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	if events := liveToolCallEvents(t, s, [2]string{"call_1", "whiteboard_manage_board"}); len(events) != 1 {
		t.Fatalf("unexpected tool calls: %+v", events)
	}
	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type":     liveResponseFailed,
			"response": map[string]any{"error": map[string]any{"message": "backend exploded"}},
		}},
	})
	drainRealtimeEvents(t, s.Events())

	if err = s.SendToolResult("call_1", "whiteboard_manage_board", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	if events := conn.sentEvents(); len(events) != 2 || events[1].OfResponseCreate == nil {
		t.Fatalf("a dropped tool turn must not block the next result: %+v", events)
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

func TestLiveSessionNoiseDoesNotBargeIn(t *testing.T) {
	conn := &fakeLiveConn{}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString([]byte{1, 2})},
	})
	drainRealtimeEvents(t, s.Events())

	// Keyboard clicks transcribe as punctuation, whitespace or a single stray
	// character: they must not cut the agent off mid-sentence.
	for _, delta := range []string{".", " ", "...", ",", "?", "a"} {
		s.handleServerEvent(live.ServerEventUnion{
			Type:                        liveEventInputTranscript,
			OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: delta},
		})
		for _, ev := range drainRealtimeEvents(t, s.Events()) {
			if ev.Type == insights.RealtimeEventInterrupted {
				t.Fatalf("noise delta %q must not report a barge-in", delta)
			}
		}
	}

	// Real speech still reports the barge-in, and only once.
	s.handleServerEvent(live.ServerEventUnion{
		Type:                        liveEventInputTranscript,
		OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: "wait"},
	})
	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 || events[0].Type != insights.RealtimeEventInterrupted {
		t.Fatalf("expected a single barge-in: %+v", events)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:                        liveEventInputTranscript,
		OfInputTranscriptDeltaEvent: live.InputTranscriptDeltaEvent{Delta: "more"},
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

	// A tool call is only released once its response completed, and an item
	// without an id still yields the default arguments.
	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseOutputItemDone,
			"item": map[string]any{"type": liveItemTypeFunctionCall, "id": "call-2", "name": "create_quiz_or_poll"},
		}},
	})
	if events = drainRealtimeEvents(t, s.Events()); len(events) != 0 {
		t.Fatalf("a tool call must wait for the completed response: %+v", events)
	}

	s.handleServerEvent(live.ServerEventUnion{
		Type:            liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{"type": liveResponseCompleted}},
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

func TestLiveSessionServerDelegationUsageDetails(t *testing.T) {
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
				"usage": map[string]any{
					"input_tokens": float64(1000),
					"input_tokens_details": map[string]any{
						"cached_tokens":              float64(200),
						"cache_write_tokens":         float64(100),
						"text_tokens":                float64(600),
						"audio_tokens":               float64(300),
						"image_tokens":               float64(100),
						"cached_tokens_details":      map[string]any{"text_tokens": float64(150), "audio_tokens": float64(0), "image_tokens": float64(50)},
						"cache_write_tokens_details": map[string]any{"text_tokens": float64(100)},
					},
					"output_tokens": float64(400),
					"output_tokens_details": map[string]any{
						"text_tokens":      float64(280),
						"audio_tokens":     float64(0),
						"reasoning_tokens": float64(120),
					},
				},
			},
		}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("expected a usage event and a turn complete: %+v", events)
	}
	usage := events[0]
	want := insights.RealtimeEvent{
		Type:                                insights.RealtimeEventUsage,
		DelegationInputTokens:               1000,
		DelegationOutputTokens:              400,
		DelegationCachedInputTokens:         200,
		DelegationCacheWriteInputTokens:     100,
		DelegationTextInputTokens:           600,
		DelegationAudioInputTokens:          300,
		DelegationImageInputTokens:          100,
		DelegationCachedTextInputTokens:     150,
		DelegationCachedImageInputTokens:    50,
		DelegationCacheWriteTextInputTokens: 100,
		DelegationTextOutputTokens:          280,
		DelegationThinkingOutputTokens:      120,
	}
	if usage.DelegationCachedInputTokens != want.DelegationCachedInputTokens ||
		usage.DelegationCacheWriteInputTokens != want.DelegationCacheWriteInputTokens ||
		usage.DelegationTextInputTokens != want.DelegationTextInputTokens ||
		usage.DelegationAudioInputTokens != want.DelegationAudioInputTokens ||
		usage.DelegationImageInputTokens != want.DelegationImageInputTokens ||
		usage.DelegationCachedTextInputTokens != want.DelegationCachedTextInputTokens ||
		usage.DelegationCachedImageInputTokens != want.DelegationCachedImageInputTokens ||
		usage.DelegationCacheWriteTextInputTokens != want.DelegationCacheWriteTextInputTokens ||
		usage.DelegationTextOutputTokens != want.DelegationTextOutputTokens ||
		usage.DelegationThinkingOutputTokens != want.DelegationThinkingOutputTokens {
		t.Fatalf("usage details = %+v, want %+v", usage, want)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Fatalf("delegated tokens must not be reported as session tokens: %+v", usage)
	}

	// The "token" spelling and the cache creation name are accepted too.
	s.handleServerEvent(live.ServerEventUnion{
		Type: liveEventResponse,
		OfResponseEvent: live.ResponseEvent{Event: map[string]any{
			"type": liveResponseCompleted,
			"response": map[string]any{
				"usage": map[string]any{
					"input_tokens": float64(10),
					"input_token_details": map[string]any{
						"cached_tokens":               float64(2),
						"cache_creation_input_tokens": float64(4),
					},
					"output_tokens":        float64(5),
					"output_token_details": map[string]any{"reasoning_tokens": float64(3)},
				},
			},
		}},
	})
	events = drainRealtimeEvents(t, s.Events())
	if len(events) != 2 {
		t.Fatalf("expected a usage event and a turn complete: %+v", events)
	}
	usage = events[0]
	if usage.DelegationCachedInputTokens != 2 || usage.DelegationCacheWriteInputTokens != 4 || usage.DelegationThinkingOutputTokens != 3 {
		t.Fatalf("unexpected usage event: %+v", usage)
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

// Closing while the idle timer and the grouper can still fire must neither panic
// with a send on a closed channel nor race on the event stream.
func TestLiveSessionCloseWithCallbacksInFlight(t *testing.T) {
	restoreLiveSpeakingIdle(t, time.Millisecond)

	conn := &fakeLiveConn{block: make(chan struct{})}
	s, err := newLiveSession(conn, nil)
	if err != nil {
		t.Fatalf("newLiveSession: %v", err)
	}
	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(context.Background(), 0)

	audio := live.ServerEventUnion{
		Type:                    liveEventOutputAudioDelta,
		OfOutputAudioDeltaEvent: live.OutputAudioDeltaEvent{Delta: base64.StdEncoding.EncodeToString([]byte{1, 2})},
	}
	transcript := live.ServerEventUnion{
		Type:                         liveEventOutputTranscript,
		OfOutputTranscriptDeltaEvent: live.OutputTranscriptDeltaEvent{EventID: "evt_1", Delta: "hi", StartMs: 0, EndMs: 200},
	}
	for i := 0; i < 20; i++ {
		s.handleServerEvent(audio)
		s.handleServerEvent(transcript)
	}

	if err = s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for range s.Events() {
	}

	// Callbacks that fire after the stream is closed must stay harmless.
	s.handleServerEvent(audio)
	s.onTranscriptUpdate(live.TranscriptUpdate{Segment: live.TranscriptSegment{Speaker: "assistant", Text: "bye"}, Reason: "inactivity"})
	s.endIdleTurn()
	if s.isSpeaking() {
		t.Fatal("a closed session must not stay speaking")
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
