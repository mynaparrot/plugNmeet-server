package google

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

// newTestLiveSession builds a session with a valid logger, matching the entry
// the runtime injects.
func newTestLiveSession(conn liveConn) *liveSession {
	return newLiveSession(conn, logrus.NewEntry(logrus.New()))
}

// fakeConn records writes and replays scripted server messages.
type fakeConn struct {
	mu        sync.Mutex
	incoming  []*genai.LiveServerMessage
	realtime  []genai.LiveRealtimeInput
	contents  []genai.LiveClientContentInput
	tools     []genai.LiveToolResponseInput
	block     chan struct{}
	closed    bool
	writeFail error
}

func (c *fakeConn) SendRealtimeInput(in genai.LiveRealtimeInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeFail != nil {
		return c.writeFail
	}
	c.realtime = append(c.realtime, in)
	return nil
}

func (c *fakeConn) SendClientContent(in genai.LiveClientContentInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeFail != nil {
		return c.writeFail
	}
	c.contents = append(c.contents, in)
	return nil
}

func (c *fakeConn) SendToolResponse(in genai.LiveToolResponseInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeFail != nil {
		return c.writeFail
	}
	c.tools = append(c.tools, in)
	return nil
}

func (c *fakeConn) Receive() (*genai.LiveServerMessage, error) {
	c.mu.Lock()
	if len(c.incoming) == 0 {
		block := c.block
		c.mu.Unlock()
		if block != nil {
			<-block
		}
		return nil, io.EOF
	}
	msg := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	return msg, nil
}

func (c *fakeConn) Close() error {
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

func TestBuildLiveConnectConfig(t *testing.T) {
	opts := &insights.RealtimeSessionOptions{
		SystemInstruction: "You are a tutor.",
		Voice:             "Aoede",
		SpokenLang:        "en-US",
		Tools: []*insights.MCPToolSpec{
			{
				Name:        "whiteboard_write_content",
				Description: "Write on the board",
				InputSchema: map[string]any{"type": "object"},
			},
			nil,
			{Name: ""},
		},
	}

	cfg := buildLiveConnectConfig(opts)

	if len(cfg.ResponseModalities) != 1 || cfg.ResponseModalities[0] != genai.ModalityAudio {
		t.Fatalf("unexpected response modalities: %v", cfg.ResponseModalities)
	}
	if cfg.InputAudioTranscription == nil || cfg.OutputAudioTranscription == nil {
		t.Fatal("transcriptions must be enabled")
	}
	if cfg.RealtimeInputConfig == nil || cfg.RealtimeInputConfig.AutomaticActivityDetection == nil {
		t.Fatalf("automatic activity detection not configured: %+v", cfg.RealtimeInputConfig)
	}
	activity := cfg.RealtimeInputConfig.AutomaticActivityDetection
	if activity.StartOfSpeechSensitivity != genai.StartSensitivityLow {
		t.Fatalf("unexpected start sensitivity: %s", activity.StartOfSpeechSensitivity)
	}
	if activity.PrefixPaddingMs == nil || *activity.PrefixPaddingMs != 300 {
		t.Fatalf("unexpected prefix padding: %v", activity.PrefixPaddingMs)
	}
	if activity.SilenceDurationMs == nil || *activity.SilenceDurationMs != 500 {
		t.Fatalf("unexpected silence duration: %v", activity.SilenceDurationMs)
	}
	if cfg.SystemInstruction == nil || len(cfg.SystemInstruction.Parts) != 1 || cfg.SystemInstruction.Parts[0].Text != "You are a tutor." {
		t.Fatalf("system instruction not mapped: %+v", cfg.SystemInstruction)
	}
	if cfg.SpeechConfig == nil || cfg.SpeechConfig.LanguageCode != "en-US" {
		t.Fatalf("speech config language not mapped: %+v", cfg.SpeechConfig)
	}
	if cfg.SpeechConfig.VoiceConfig == nil || cfg.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig == nil {
		t.Fatalf("voice not mapped: %+v", cfg.SpeechConfig)
	}
	if got := cfg.SpeechConfig.VoiceConfig.PrebuiltVoiceConfig.VoiceName; got != "Aoede" {
		t.Fatalf("unexpected voice: %s", got)
	}
	if len(cfg.Tools) != 1 || len(cfg.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("tools not mapped: %+v", cfg.Tools)
	}
	if name := cfg.Tools[0].FunctionDeclarations[0].Name; name != "whiteboard_write_content" {
		t.Fatalf("unexpected declaration name: %s", name)
	}
}

func TestBuildLiveConnectConfigMinimal(t *testing.T) {
	cfg := buildLiveConnectConfig(&insights.RealtimeSessionOptions{})

	if cfg.SystemInstruction != nil {
		t.Fatal("empty options must not set a system instruction")
	}
	if cfg.SpeechConfig != nil {
		t.Fatal("empty options must not set a speech config")
	}
	if len(cfg.Tools) != 0 {
		t.Fatal("empty options must not declare tools")
	}
}

func TestBuildFunctionDeclarations(t *testing.T) {
	decls := buildFunctionDeclarations([]*insights.MCPToolSpec{
		{Name: "a", Description: "first"},
		{Name: "b", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	})

	if len(decls) != 2 {
		t.Fatalf("expected 2 declarations, got %d", len(decls))
	}
	if decls[0].ParametersJsonSchema != nil {
		t.Fatal("declaration without schema must not set a schema")
	}
	if decls[1].ParametersJsonSchema == nil {
		t.Fatal("schema was not passed through")
	}
}

func TestTranslateServerMessage(t *testing.T) {
	msg := &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{
			Interrupted:               true,
			InputTranscription:        &genai.Transcription{Text: "hello", Finished: true},
			OutputTranscription:       &genai.Transcription{Text: "hi there", Finished: true},
			InterimInputTranscription: &genai.Transcription{Text: "hel"},
			ModelTurn: &genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{InlineData: &genai.Blob{MIMEType: "audio/pcm;rate=24000", Data: []byte{1, 2, 3, 4}}},
					nil,
					{Text: "ignored"},
				},
			},
			TurnComplete: true,
		},
		ToolCall: &genai.LiveServerToolCall{
			FunctionCalls: []*genai.FunctionCall{
				{ID: "call-1", Name: "create_quiz_or_poll", Args: map[string]any{"question": "2+2?"}},
				nil,
			},
		},
		UsageMetadata: &genai.UsageMetadata{PromptTokenCount: 11, ResponseTokenCount: 7, CachedContentTokenCount: 4},
		GoAway:        &genai.LiveServerGoAway{TimeLeft: 10 * time.Second},
	}

	events := translateServerMessage(msg)

	var types []insights.RealtimeEventType
	for _, ev := range events {
		types = append(types, ev.Type)

		switch ev.Type {
		case insights.RealtimeEventAudioDelta:
			if string(ev.AudioPCM24k) != string([]byte{1, 2, 3, 4}) {
				t.Fatalf("unexpected audio: %v", ev.AudioPCM24k)
			}
		case insights.RealtimeEventTranscriptUser:
			if ev.Text != "hello" && ev.Text != "hel" {
				t.Fatalf("unexpected user transcript: %q", ev.Text)
			}
		case insights.RealtimeEventTranscriptAgent:
			if ev.Text != "hi there" || !ev.Final {
				t.Fatalf("unexpected agent transcript: %+v", ev)
			}
		case insights.RealtimeEventToolCall:
			if ev.CallID != "call-1" || ev.ToolName != "create_quiz_or_poll" {
				t.Fatalf("unexpected tool call: %+v", ev)
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(ev.ArgsJSON), &args); err != nil {
				t.Fatalf("args are not JSON: %v", err)
			}
			if args["question"] != "2+2?" {
				t.Fatalf("unexpected args: %v", args)
			}
		case insights.RealtimeEventUsage:
			if ev.InputTokens != 11 || ev.OutputTokens != 7 || ev.CachedInputTokens != 4 {
				t.Fatalf("unexpected usage: %+v", ev)
			}
		}
	}

	want := []insights.RealtimeEventType{
		insights.RealtimeEventInterrupted,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventTranscriptAgent,
		insights.RealtimeEventAudioDelta,
		insights.RealtimeEventTurnComplete,
		insights.RealtimeEventToolCall,
		insights.RealtimeEventUsage,
		insights.RealtimeEventGoAway,
	}
	if len(types) != len(want) {
		t.Fatalf("expected %d events, got %d: %v", len(want), len(types), types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("event %d: expected %s, got %s", i, want[i], types[i])
		}
	}
}

func TestTranslateUsageMetadata(t *testing.T) {
	// Text, audio and video input, the cached subset of text and audio, an
	// audio-only response, plus thinking and tool result tokens, which Gemini
	// reports beside the prompt and the response counts.
	ev := translateUsageMetadata(&genai.UsageMetadata{
		PromptTokenCount:        100,
		CachedContentTokenCount: 40,
		ResponseTokenCount:      20,
		ThoughtsTokenCount:      5,
		ToolUsePromptTokenCount: 8,
		PromptTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: 60},
			{Modality: genai.MediaModalityAudio, TokenCount: 25},
			{Modality: genai.MediaModalityVideo, TokenCount: 15},
			nil,
		},
		ToolUsePromptTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: 8},
		},
		CacheTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: 30},
			{Modality: genai.MediaModalityAudio, TokenCount: 10},
		},
		ResponseTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityAudio, TokenCount: 20},
		},
	})

	if ev.Type != insights.RealtimeEventUsage {
		t.Fatalf("unexpected event type: %s", ev.Type)
	}
	// Tool results are input and thinking tokens are output.
	if ev.InputTokens != 108 || ev.OutputTokens != 25 || ev.CachedInputTokens != 40 {
		t.Errorf("totals = (%d, %d, %d), want (108, 25, 40)", ev.InputTokens, ev.OutputTokens, ev.CachedInputTokens)
	}
	if ev.TextInputTokens != 68 || ev.AudioInputTokens != 25 || ev.ImageInputTokens != 15 {
		t.Errorf("input split = (%d, %d, %d), want (68, 25, 15)",
			ev.TextInputTokens, ev.AudioInputTokens, ev.ImageInputTokens)
	}
	if ev.CachedTextInputTokens != 30 || ev.CachedAudioInputTokens != 10 || ev.CachedImageInputTokens != 0 {
		t.Errorf("cached split = (%d, %d, %d), want (30, 10, 0)",
			ev.CachedTextInputTokens, ev.CachedAudioInputTokens, ev.CachedImageInputTokens)
	}
	if ev.TextOutputTokens != 5 || ev.AudioOutputTokens != 20 || ev.ThinkingOutputTokens != 5 {
		t.Errorf("output split = (%d, %d, thinking %d), want (5, 20, thinking 5)",
			ev.TextOutputTokens, ev.AudioOutputTokens, ev.ThinkingOutputTokens)
	}

	// A text turn reports no detail at all: everything is text.
	text := translateUsageMetadata(&genai.UsageMetadata{PromptTokenCount: 12, ResponseTokenCount: 7})
	if text.InputTokens != 12 || text.OutputTokens != 7 || text.TextOutputTokens != 7 {
		t.Errorf("unexpected text usage: %+v", text)
	}
	if text.AudioInputTokens != 0 || text.ImageInputTokens != 0 || text.ThinkingOutputTokens != 0 {
		t.Errorf("a text turn must not report audio, image or thinking tokens: %+v", text)
	}
}

func TestTranslateServerMessageEmpty(t *testing.T) {
	if events := translateServerMessage(nil); len(events) != 0 {
		t.Fatalf("nil message must not emit events, got %d", len(events))
	}
	if events := translateServerMessage(&genai.LiveServerMessage{SetupComplete: &genai.LiveServerSetupComplete{}}); len(events) != 0 {
		t.Fatalf("setup complete must not emit events, got %d", len(events))
	}
}

func TestFunctionResponsePayload(t *testing.T) {
	ok := functionResponsePayload(`{"ok":true}`, false)
	if _, hasError := ok["error"]; hasError {
		t.Fatal("success must not use the error key")
	}
	if parsed, isMap := ok["output"].(map[string]any); !isMap || parsed["ok"] != true {
		t.Fatalf("output was not parsed as JSON: %v", ok["output"])
	}

	failed := functionResponsePayload(`{"reason":"no_delegate"}`, true)
	if _, isMap := failed["error"].(map[string]any); !isMap {
		t.Fatalf("error payload was not parsed as JSON: %v", failed["error"])
	}

	raw := functionResponsePayload("not json", false)
	if raw["output"] != "not json" {
		t.Fatalf("invalid JSON must be forwarded as a string: %v", raw["output"])
	}
}

func TestLiveSessionSend(t *testing.T) {
	conn := &fakeConn{}
	s := newTestLiveSession(conn)

	if err := s.SendAudio([]byte{1, 2}); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}
	if len(conn.realtime) != 1 || conn.realtime[0].Audio == nil {
		t.Fatalf("audio not forwarded: %+v", conn.realtime)
	}
	if got := conn.realtime[0].Audio.MIMEType; got != liveAudioMIMEType {
		t.Fatalf("unexpected audio mime type: %s", got)
	}

	if err := s.SendImage("image/webp", []byte("img"), "look here"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	if len(conn.realtime) != 2 || conn.realtime[1].Video == nil || conn.realtime[1].Video.MIMEType != "image/webp" {
		t.Fatalf("image not forwarded: %+v", conn.realtime)
	}
	if len(conn.contents) != 1 || conn.contents[0].Turns[0].Parts[0].Text != "look here" {
		t.Fatalf("caption not forwarded: %+v", conn.contents)
	}
	if conn.contents[0].TurnComplete == nil || *conn.contents[0].TurnComplete {
		t.Fatal("caption must not trigger a response")
	}

	if err := s.SendContextText("", "poll submitted", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if len(conn.contents) != 2 {
		t.Fatalf("context text not forwarded: %+v", conn.contents)
	}
	if role := conn.contents[1].Turns[0].Role; role != genai.RoleUser {
		t.Fatalf("empty role must default to user, got %s", role)
	}
	if conn.contents[1].TurnComplete == nil || !*conn.contents[1].TurnComplete {
		t.Fatal("triggerResponse must set TurnComplete")
	}

	if err := s.SendToolResult("call-1", "get_poll_results", `{"votes":3}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	if len(conn.tools) != 1 || len(conn.tools[0].FunctionResponses) != 1 {
		t.Fatalf("tool result not forwarded: %+v", conn.tools)
	}
	response := conn.tools[0].FunctionResponses[0]
	if response.ID != "call-1" || response.Name != "get_poll_results" {
		t.Fatalf("unexpected function response: %+v", response)
	}
	if _, ok := response.Response["output"]; !ok {
		t.Fatalf("function response payload missing output: %+v", response.Response)
	}

	if err := s.SendToolResult("call-2", "", "{}", true); err == nil {
		t.Fatal("a tool result without a tool name must fail")
	}

	if err := s.Interrupt(); err != nil {
		t.Fatalf("Interrupt must be a no-op: %v", err)
	}

	if err := s.SendContextText("user", "", true); err != nil {
		t.Fatalf("empty context text must be a no-op: %v", err)
	}
	if len(conn.contents) != 2 {
		t.Fatalf("empty context text must not be forwarded: %+v", conn.contents)
	}
}

func TestLiveSessionSendAfterClose(t *testing.T) {
	conn := &fakeConn{}
	s := newTestLiveSession(conn)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.SendAudio([]byte{1}); !errors.Is(err, errLiveSessionClosed) {
		t.Fatalf("expected errLiveSessionClosed, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
}

func TestLiveSessionSendError(t *testing.T) {
	conn := &fakeConn{writeFail: errors.New("socket is broken")}
	s := newTestLiveSession(conn)

	if err := s.SendAudio([]byte{1}); err == nil {
		t.Fatal("send errors must be reported")
	}
}

func TestLiveSessionReadLoop(t *testing.T) {
	conn := &fakeConn{incoming: []*genai.LiveServerMessage{
		{ServerContent: &genai.LiveServerContent{ModelTurn: &genai.Content{
			Parts: []*genai.Part{{InlineData: &genai.Blob{Data: []byte{9, 9}}}},
		}}},
		{ServerContent: &genai.LiveServerContent{TurnComplete: true}},
	}}
	s := newTestLiveSession(conn)
	s.wg.Add(1)
	go s.readLoop()

	var events []insights.RealtimeEvent
	for ev := range s.Events() {
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(events), events)
	}
	if events[0].Type != insights.RealtimeEventAudioDelta || len(events[0].AudioPCM24k) != 2 {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Type != insights.RealtimeEventError || events[2].Err == nil {
		t.Fatalf("a read error must be reported: %+v", events[2])
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLiveSessionMaxDurationEmitsGoAway(t *testing.T) {
	conn := &fakeConn{block: make(chan struct{})}
	s := newTestLiveSession(conn)
	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(context.Background(), 5*time.Millisecond)

	select {
	case ev := <-s.Events():
		if ev.Type != insights.RealtimeEventGoAway {
			t.Fatalf("expected go_away, got %+v", ev)
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
