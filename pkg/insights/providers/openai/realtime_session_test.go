package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/openai/openai-go/v3/realtime"
)

// fakeRealtimeAgentConn records writes and replays scripted server events.
type fakeRealtimeAgentConn struct {
	mu        sync.Mutex
	incoming  []realtime.RealtimeServerEventUnion
	sent      []realtime.RealtimeClientEventUnionParam
	block     chan struct{}
	closed    bool
	writeFail error
}

func (c *fakeRealtimeAgentConn) Send(_ context.Context, event realtime.RealtimeClientEventUnionParam) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeFail != nil {
		return c.writeFail
	}
	c.sent = append(c.sent, event)
	return nil
}

func (c *fakeRealtimeAgentConn) Recv(_ context.Context) (realtime.RealtimeServerEventUnion, error) {
	c.mu.Lock()
	if len(c.incoming) == 0 {
		block := c.block
		c.mu.Unlock()
		if block != nil {
			<-block
		}
		return realtime.RealtimeServerEventUnion{}, io.EOF
	}
	msg := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	return msg, nil
}

func (c *fakeRealtimeAgentConn) Close() error {
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

func (c *fakeRealtimeAgentConn) sentEvents() []realtime.RealtimeClientEventUnionParam {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := make([]realtime.RealtimeClientEventUnionParam, len(c.sent))
	copy(events, c.sent)
	return events
}

func TestIsLiveModel(t *testing.T) {
	tests := map[string]bool{
		"":                      false,
		"gpt-realtime-2.1-mini": false,
		"gpt-realtime-2.1":      false,
		"Qwen/Qwen2.5-Omni-7B":  false,
		"gpt-live-1":            true,
		" GPT-Live-1 ":          true,
	}

	for model, want := range tests {
		if got := isLiveModel(model); got != want {
			t.Fatalf("isLiveModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestResolveRealtimeModel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		service *config.ServiceConfig
		want    string
	}{
		{name: "explicit model wins", model: " gpt-realtime-2.1 ", service: nil, want: "gpt-realtime-2.1"},
		{name: "no service uses the default", want: defaultRealtimeAgentModel},
		{
			name:    "service model",
			service: &config.ServiceConfig{Options: map[string]interface{}{"model": "gpt-5.4-mini"}},
			want:    "gpt-5.4-mini",
		},
		{
			name: "realtime model beats model",
			service: &config.ServiceConfig{Options: map[string]interface{}{
				"model":          "gpt-5.4-mini",
				"realtime_model": "gpt-live-1",
			}},
			want: "gpt-live-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &OpenAIProvider{service: tc.service}
			if got := p.resolveRealtimeModel(tc.model); got != tc.want {
				t.Fatalf("resolveRealtimeModel(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

func TestBuildRealtimeSessionConfigDefaults(t *testing.T) {
	cfg := buildRealtimeSessionConfig(
		defaultRealtimeAgentModel,
		&insights.RealtimeSessionOptions{},
		transcriptionTurnDetectionSemanticVAD,
		defaultRealtimeInputTranscriptionModel,
		defaultRealtimeAgentVoice,
	)

	if cfg.Model != realtime.RealtimeSessionCreateRequestModelGPTRealtime2_1Mini {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if len(cfg.OutputModalities) != 1 || cfg.OutputModalities[0] != "audio" {
		t.Fatalf("unexpected output modalities: %v", cfg.OutputModalities)
	}
	if cfg.Instructions.Valid() {
		t.Fatal("empty options must not set instructions")
	}

	input := cfg.Audio.Input
	if input.Format.OfAudioPCM == nil || input.Format.OfAudioPCM.Rate != 24000 || input.Format.OfAudioPCM.Type != "audio/pcm" {
		t.Fatalf("unexpected input format: %+v", input.Format)
	}
	if input.Transcription.Model != realtime.AudioTranscriptionModelGPTTranscribe {
		t.Fatalf("unexpected transcription model: %s", input.Transcription.Model)
	}
	if input.Transcription.Language.Valid() {
		t.Fatal("empty spoken language must not set a transcription language")
	}
	if input.NoiseReduction.Type != realtime.NoiseReductionTypeNearField {
		t.Fatalf("unexpected noise reduction: %+v", input.NoiseReduction)
	}

	vad := input.TurnDetection.OfSemanticVad
	if vad == nil || vad.Eagerness != "low" {
		t.Fatalf("semantic vad not configured: %+v", vad)
	}
	if !vad.CreateResponse.Or(false) || !vad.InterruptResponse.Or(false) {
		t.Fatalf("semantic vad must create and interrupt responses: %+v", vad)
	}

	output := cfg.Audio.Output
	if output.Format.OfAudioPCM == nil || output.Format.OfAudioPCM.Rate != 24000 {
		t.Fatalf("unexpected output format: %+v", output.Format)
	}
	if output.Voice.OfString.Or("") != defaultRealtimeAgentVoice {
		t.Fatalf("unexpected voice: %v", output.Voice.OfString)
	}
	if output.Speed.Valid() {
		t.Fatal("no speaking rate means no speed")
	}
	if len(cfg.Tools) != 0 {
		t.Fatalf("empty options must not declare tools: %v", cfg.Tools)
	}
}

func TestBuildRealtimeSessionConfigOptions(t *testing.T) {
	cfg := buildRealtimeSessionConfig(
		"gpt-realtime-2.1",
		&insights.RealtimeSessionOptions{
			SystemInstruction: "You are a tutor.",
			SpokenLang:        "en-US",
			SpeakingRate:      3,
			Tools: []*insights.MCPToolSpec{
				{
					Name:        "whiteboard_write_content",
					Description: "Write on the board",
					InputSchema: map[string]any{"type": "object"},
				},
				nil,
				{Name: ""},
			},
		},
		transcriptionTurnDetectionServerVAD,
		"whisper-1",
		"cedar",
	)

	if cfg.Model != realtime.RealtimeSessionCreateRequestModel("gpt-realtime-2.1") {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Instructions.Or("") != "You are a tutor." {
		t.Fatalf("instructions not mapped: %v", cfg.Instructions)
	}
	if lang := cfg.Audio.Input.Transcription.Language.Or(""); lang != "en" {
		t.Fatalf("locale must normalize to ISO-639-1, got %q", lang)
	}
	if cfg.Audio.Input.Transcription.Model != realtime.AudioTranscriptionModel("whisper-1") {
		t.Fatalf("unexpected transcription model: %s", cfg.Audio.Input.Transcription.Model)
	}
	serverVad := cfg.Audio.Input.TurnDetection.OfServerVad
	if serverVad == nil {
		t.Fatal("server_vad must configure server vad")
	}
	if cfg.Audio.Input.TurnDetection.OfSemanticVad != nil {
		t.Fatal("server_vad must not configure semantic vad")
	}
	if got := serverVad.Threshold.Or(0); got != 0.65 {
		t.Fatalf("unexpected server vad threshold: %v", got)
	}
	if got := serverVad.PrefixPaddingMs.Or(0); got != 300 {
		t.Fatalf("unexpected server vad prefix padding: %v", got)
	}
	if got := serverVad.SilenceDurationMs.Or(0); got != 600 {
		t.Fatalf("unexpected server vad silence duration: %v", got)
	}
	if got := cfg.Audio.Output.Speed.Or(0); got != maxRealtimeSpeakingRate {
		t.Fatalf("speaking rate must be clamped to %v, got %v", maxRealtimeSpeakingRate, got)
	}
	if cfg.Audio.Output.Voice.OfString.Or("") != "cedar" {
		t.Fatalf("unexpected voice: %v", cfg.Audio.Output.Voice.OfString)
	}

	if len(cfg.Tools) != 1 || cfg.Tools[0].OfFunction == nil {
		t.Fatalf("tools not mapped: %+v", cfg.Tools)
	}
	function := cfg.Tools[0].OfFunction
	if function.Name.Or("") != "whiteboard_write_content" || function.Type != realtime.RealtimeFunctionToolTypeFunction {
		t.Fatalf("unexpected function tool: %+v", function)
	}
	if function.Parameters == nil {
		t.Fatal("input schema was not passed through")
	}
}

func TestBuildRealtimeSessionConfigDisabledFeatures(t *testing.T) {
	cfg := buildRealtimeSessionConfig(
		defaultRealtimeAgentModel,
		&insights.RealtimeSessionOptions{SpeakingRate: 0.1},
		transcriptionTurnDetectionNone,
		defaultRealtimeInputTranscriptionModel,
		"",
	)

	if cfg.Audio.Input.TurnDetection.OfSemanticVad != nil || cfg.Audio.Input.TurnDetection.OfServerVad != nil {
		t.Fatalf("disabled turn detection must stay omitted: %+v", cfg.Audio.Input.TurnDetection)
	}
	if got := cfg.Audio.Output.Speed.Or(0); got != minRealtimeSpeakingRate {
		t.Fatalf("speaking rate must be clamped to %v, got %v", minRealtimeSpeakingRate, got)
	}
	if cfg.Audio.Output.Voice.OfString.Valid() {
		t.Fatal("empty voice must not be set")
	}

	noTranscription := buildRealtimeSessionConfig(
		defaultRealtimeAgentModel,
		&insights.RealtimeSessionOptions{},
		transcriptionTurnDetectionManual,
		"none",
		defaultRealtimeAgentVoice,
	)
	if noTranscription.Audio.Input.Transcription.Model != "" {
		t.Fatalf("transcription must stay off, got %s", noTranscription.Audio.Input.Transcription.Model)
	}
}

func TestTranslateRealtimeServerEventAudioAndSpeech(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}

	events := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.output_audio.delta",
		OfResponseAudioDeltaEvent: realtime.ResponseAudioDeltaEvent{
			Delta: base64.StdEncoding.EncodeToString(pcm),
		},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventAudioDelta || string(events[0].AudioPCM24k) != string(pcm) {
		t.Fatalf("unexpected audio events: %+v", events)
	}

	events = translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type:                                 "input_audio_buffer.speech_started",
		OfInputAudioBufferSpeechStartedEvent: realtime.InputAudioBufferSpeechStartedEvent{},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventInterrupted {
		t.Fatalf("speech started must interrupt: %+v", events)
	}

	if events = translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "session.created",
	}); len(events) != 0 {
		t.Fatalf("unrelated events must stay silent: %+v", events)
	}
}

func TestTranslateRealtimeServerEventTranscripts(t *testing.T) {
	events := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "conversation.item.input_audio_transcription.delta",
		OfConversationItemInputAudioTranscriptionDeltaEvent: realtime.ConversationItemInputAudioTranscriptionDeltaEvent{
			Delta: "hel",
		},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTranscriptUser || events[0].Text != "hel" || events[0].Final {
		t.Fatalf("unexpected user delta: %+v", events)
	}

	events = translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "conversation.item.input_audio_transcription.completed",
		OfConversationItemInputAudioTranscriptionCompletedEvent: realtime.ConversationItemInputAudioTranscriptionCompletedEvent{
			Transcript: "hello",
		},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTranscriptUser || events[0].Text != "hello" || !events[0].Final {
		t.Fatalf("unexpected user transcript: %+v", events)
	}

	events = translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.output_audio_transcript.delta",
		OfResponseAudioTranscriptDeltaEvent: realtime.ResponseAudioTranscriptDeltaEvent{
			Delta: "hi ",
		},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTranscriptAgent || events[0].Text != "hi " || events[0].Final {
		t.Fatalf("unexpected agent delta: %+v", events)
	}

	events = translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.output_audio_transcript.done",
		OfResponseAudioTranscriptDoneEvent: realtime.ResponseAudioTranscriptDoneEvent{
			Transcript: "hi there",
		},
	})
	if len(events) != 1 || events[0].Type != insights.RealtimeEventTranscriptAgent || events[0].Text != "hi there" || !events[0].Final {
		t.Fatalf("unexpected agent transcript: %+v", events)
	}
}

func TestTranslateRealtimeServerEventToolCall(t *testing.T) {
	events := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.function_call_arguments.done",
		OfResponseFunctionCallArgumentsDoneEvent: realtime.ResponseFunctionCallArgumentsDoneEvent{
			CallID:    "call-1",
			Name:      "get_poll_results",
			Arguments: `{"poll_id":"1"}`,
		},
	})

	if len(events) != 1 {
		t.Fatalf("expected one tool call, got %+v", events)
	}
	event := events[0]
	if event.Type != insights.RealtimeEventToolCall || event.CallID != "call-1" || event.ToolName != "get_poll_results" {
		t.Fatalf("unexpected tool call: %+v", event)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(event.ArgsJSON), &args); err != nil || args["poll_id"] != "1" {
		t.Fatalf("unexpected tool args: %q (%v)", event.ArgsJSON, err)
	}

	empty := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type:                                     "response.function_call_arguments.done",
		OfResponseFunctionCallArgumentsDoneEvent: realtime.ResponseFunctionCallArgumentsDoneEvent{CallID: "call-2"},
	})
	if len(empty) != 1 || empty[0].ArgsJSON != "{}" {
		t.Fatalf("empty arguments must default to {}: %+v", empty)
	}
}

func TestTranslateRealtimeServerEventResponseDone(t *testing.T) {
	usageOnly := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
			Usage:  realtime.RealtimeResponseUsage{InputTokens: 11, OutputTokens: 7},
			Output: []realtime.ConversationItemUnion{{Type: "function_call"}},
		}},
	})
	if len(usageOnly) != 1 {
		t.Fatalf("a function call turn must not complete the turn: %+v", usageOnly)
	}
	if usageOnly[0].Type != insights.RealtimeEventUsage || usageOnly[0].InputTokens != 11 || usageOnly[0].OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", usageOnly[0])
	}

	completed := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
			Usage:  realtime.RealtimeResponseUsage{InputTokens: 3, OutputTokens: 2},
			Output: []realtime.ConversationItemUnion{{Type: "message"}},
		}},
	})
	if len(completed) != 2 || completed[1].Type != insights.RealtimeEventTurnComplete {
		t.Fatalf("completed response must emit turn complete: %+v", completed)
	}

	// Failure details only arrive as raw JSON, so decode a real payload.
	var failed realtime.RealtimeServerEventUnion
	payload := `{"type":"response.done","event_id":"evt_1","response":{"status":"failed",` +
		`"status_details":{"type":"failed","error":{"code":"server_error","message":"model exploded"}}}}`
	if err := json.Unmarshal([]byte(payload), &failed); err != nil {
		t.Fatalf("failed to decode response.done: %v", err)
	}

	events := translateRealtimeServerEvent(failed)
	if len(events) != 1 || events[0].Type != insights.RealtimeEventError {
		t.Fatalf("failed response must emit an error: %+v", events)
	}
	if !strings.Contains(events[0].Err.Error(), "model exploded") {
		t.Fatalf("unexpected error message: %v", events[0].Err)
	}

	// Cached prompt tokens arrive nested in input_token_details.
	var cached realtime.RealtimeServerEventUnion
	cachedPayload := `{"type":"response.done","event_id":"evt_2","response":{"status":"completed",` +
		`"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150,` +
		`"input_token_details":{"cached_tokens":64,"text_tokens":120,"audio_tokens":0}}}}`
	if err := json.Unmarshal([]byte(cachedPayload), &cached); err != nil {
		t.Fatalf("failed to decode cached usage: %v", err)
	}

	cachedEvents := translateRealtimeServerEvent(cached)
	if len(cachedEvents) == 0 || cachedEvents[0].Type != insights.RealtimeEventUsage {
		t.Fatalf("cached usage event missing: %+v", cachedEvents)
	}
	if cachedEvents[0].InputTokens != 120 || cachedEvents[0].OutputTokens != 30 || cachedEvents[0].CachedInputTokens != 64 {
		t.Fatalf("unexpected cached usage: %+v", cachedEvents[0])
	}
	if cachedEvents[0].TextInputTokens != 120 || cachedEvents[0].AudioInputTokens != 0 {
		t.Fatalf("unexpected input modality split: %+v", cachedEvents[0])
	}
}

func TestTranslateRealtimeServerEventResponseDoneModalities(t *testing.T) {
	// Text, audio and image input, the cached subset of each, and the output
	// split all arrive nested in the response usage.
	var event realtime.RealtimeServerEventUnion
	payload := `{"type":"response.done","event_id":"evt_3","response":{"status":"completed",` +
		`"usage":{"input_tokens":300,"output_tokens":120,"total_tokens":420,` +
		`"input_token_details":{"cached_tokens":80,"text_tokens":100,"audio_tokens":180,"image_tokens":20,` +
		`"cached_tokens_details":{"text_tokens":60,"audio_tokens":20,"image_tokens":0}},` +
		`"output_token_details":{"text_tokens":20,"audio_tokens":100}}}}`
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("failed to decode multimodal usage: %v", err)
	}

	events := translateRealtimeServerEvent(event)
	if len(events) == 0 || events[0].Type != insights.RealtimeEventUsage {
		t.Fatalf("usage event missing: %+v", events)
	}

	usage := events[0]
	if usage.InputTokens != 300 || usage.OutputTokens != 120 || usage.CachedInputTokens != 80 {
		t.Errorf("totals = (%d, %d, %d), want (300, 120, 80)",
			usage.InputTokens, usage.OutputTokens, usage.CachedInputTokens)
	}
	if usage.TextInputTokens != 100 || usage.AudioInputTokens != 180 || usage.ImageInputTokens != 20 {
		t.Errorf("input split = (%d, %d, %d), want (100, 180, 20)",
			usage.TextInputTokens, usage.AudioInputTokens, usage.ImageInputTokens)
	}
	if usage.CachedTextInputTokens != 60 || usage.CachedAudioInputTokens != 20 || usage.CachedImageInputTokens != 0 {
		t.Errorf("cached split = (%d, %d, %d), want (60, 20, 0)",
			usage.CachedTextInputTokens, usage.CachedAudioInputTokens, usage.CachedImageInputTokens)
	}
	if usage.TextOutputTokens != 20 || usage.AudioOutputTokens != 100 {
		t.Errorf("output split = (%d, %d), want (20, 100)", usage.TextOutputTokens, usage.AudioOutputTokens)
	}
}

func TestTranslateRealtimeServerEventError(t *testing.T) {
	events := translateRealtimeServerEvent(realtime.RealtimeServerEventUnion{
		Type:                 "error",
		OfRealtimeErrorEvent: realtime.RealtimeErrorEvent{Error: realtime.RealtimeError{Message: "invalid event", Code: "invalid_request_error"}},
	})

	if len(events) != 1 || events[0].Type != insights.RealtimeEventError || events[0].Err == nil {
		t.Fatalf("unexpected error event: %+v", events)
	}
	if !strings.Contains(events[0].Err.Error(), "invalid event") || !strings.Contains(events[0].Err.Error(), "invalid_request_error") {
		t.Fatalf("unexpected error message: %v", events[0].Err)
	}
}

func TestRealtimeAgentSessionSendAudio(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	// 1600 bytes = 800 samples at 16 kHz, which is 1200 samples at 24 kHz.
	if err := s.SendAudio(make([]byte, 1600)); err != nil {
		t.Fatalf("SendAudio: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfInputAudioBufferAppend == nil {
		t.Fatalf("audio not forwarded: %+v", events)
	}
	pcm, err := base64.StdEncoding.DecodeString(events[0].OfInputAudioBufferAppend.Audio)
	if err != nil {
		t.Fatalf("audio is not base64: %v", err)
	}
	if len(pcm) != 2400 {
		t.Fatalf("expected 2400 resampled bytes, got %d", len(pcm))
	}

	// Odd byte counts must not panic or drop audio.
	if err := s.SendAudio(make([]byte, 1601)); err != nil {
		t.Fatalf("SendAudio with odd length: %v", err)
	}
	if events := conn.sentEvents(); len(events) != 2 {
		t.Fatalf("odd length audio must still be sent: %+v", events)
	}

	if err := s.SendAudio(nil); err != nil {
		t.Fatalf("empty audio must be a no-op: %v", err)
	}
	if events := conn.sentEvents(); len(events) != 2 {
		t.Fatalf("empty audio must not be forwarded: %+v", events)
	}
}

func TestRealtimeAgentSessionSendContextText(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.SendContextText("system", "the poll is closed", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfConversationItemCreate == nil {
		t.Fatalf("context text not forwarded: %+v", events)
	}
	system := events[0].OfConversationItemCreate.Item.OfRealtimeSystemMessageItem
	if system == nil || len(system.Content) != 1 {
		t.Fatalf("silent context must be a system message: %+v", events[0].OfConversationItemCreate.Item)
	}
	if system.Content[0].Type != "input_text" || system.Content[0].Text.Or("") != "the poll is closed" {
		t.Fatalf("unexpected system content: %+v", system.Content[0])
	}

	if err := s.SendContextText("user", "summarize the poll", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}

	events = conn.sentEvents()
	if len(events) != 3 {
		t.Fatalf("triggered context must append a response.create: %+v", events)
	}
	user := events[1].OfConversationItemCreate.Item.OfRealtimeUserMessageItem
	if user == nil || len(user.Content) != 1 || user.Content[0].Type != "input_text" {
		t.Fatalf("triggered context must be a user message: %+v", events[1].OfConversationItemCreate.Item)
	}
	if events[2].OfResponseCreate == nil {
		t.Fatalf("response.create not sent: %+v", events[2])
	}

	// A system instruction that triggers a response is still a system message.
	if err := s.SendContextText("developer", "answer now", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	events = conn.sentEvents()
	if system := events[3].OfConversationItemCreate.Item.OfRealtimeSystemMessageItem; system == nil {
		t.Fatalf("developer context must stay a system message: %+v", events[3])
	}

	if err := s.SendContextText("user", "", true); err != nil {
		t.Fatalf("empty context must be a no-op: %v", err)
	}
	if events := conn.sentEvents(); len(events) != 5 {
		t.Fatalf("empty context text must not be forwarded: %+v", events)
	}
}

func TestRealtimeAgentSessionSendImage(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.SendImage("", []byte("img"), "look at the board"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 1 || events[0].OfConversationItemCreate == nil {
		t.Fatalf("image not forwarded: %+v", events)
	}
	user := events[0].OfConversationItemCreate.Item.OfRealtimeUserMessageItem
	if user == nil || len(user.Content) != 2 {
		t.Fatalf("expected image plus caption: %+v", events[0].OfConversationItemCreate.Item)
	}
	image := user.Content[0]
	if image.Type != "input_image" || !strings.HasPrefix(image.ImageURL.Or(""), "data:image/webp;base64,") || image.Detail != "auto" {
		t.Fatalf("unexpected image content: %+v", image)
	}
	if user.Content[1].Type != "input_text" || user.Content[1].Text.Or("") != "look at the board" {
		t.Fatalf("unexpected caption: %+v", user.Content[1])
	}

	if err := s.SendImage("image/png", nil, "ignored"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	if events := conn.sentEvents(); len(events) != 1 {
		t.Fatalf("empty image must not be forwarded: %+v", events)
	}

	if err := s.SendImage("image/png", []byte("img"), ""); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	events = conn.sentEvents()
	if user := events[1].OfConversationItemCreate.Item.OfRealtimeUserMessageItem; len(user.Content) != 1 {
		t.Fatalf("caption-less image must send one part: %+v", user.Content)
	}
	if !strings.HasPrefix(events[1].OfConversationItemCreate.Item.OfRealtimeUserMessageItem.Content[0].ImageURL.Or(""), "data:image/png;base64,") {
		t.Fatal("mime type was not applied")
	}
}

func TestRealtimeAgentSessionSendToolResult(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.SendToolResult("call-1", "get_poll_results", `{"votes":3}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 2 {
		t.Fatalf("tool result must be followed by a response.create: %+v", events)
	}
	output := events[0].OfConversationItemCreate.Item.OfFunctionCallOutput
	if output == nil || output.CallID != "call-1" || output.Output != `{"votes":3}` {
		t.Fatalf("unexpected function call output: %+v", output)
	}
	if events[1].OfResponseCreate == nil {
		t.Fatalf("response.create not sent: %+v", events[1])
	}

	if err := s.SendToolResult("call-2", "get_poll_results", "delegate rejected", true); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	events = conn.sentEvents()
	failed := events[2].OfConversationItemCreate.Item.OfFunctionCallOutput
	if failed == nil || !strings.Contains(failed.Output, `"error"`) || !strings.Contains(failed.Output, "delegate rejected") {
		t.Fatalf("failed tool results must be wrapped: %+v", failed)
	}

	if err := s.SendToolResult("call-3", "get_poll_results", "", false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	if events := conn.sentEvents(); events[4].OfConversationItemCreate.Item.OfFunctionCallOutput.Output != "{}" {
		t.Fatalf("empty results must default to {}: %+v", events[4])
	}
}

func realtimeToolCallEvent(callID, name string) realtime.RealtimeServerEventUnion {
	return realtime.RealtimeServerEventUnion{
		Type: "response.function_call_arguments.done",
		OfResponseFunctionCallArgumentsDoneEvent: realtime.ResponseFunctionCallArgumentsDoneEvent{
			CallID:    callID,
			Name:      name,
			Arguments: `{"a":1}`,
		},
	}
}

// A response may call several tools at once: response.create must wait until the
// last function_call_output is on the wire, and context that arrives in between
// must not jump the queue.
func TestRealtimeAgentSessionMultipleToolCallsWaitForAllOutputs(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if !s.handleServerMessage(realtimeToolCallEvent("call_1", "whiteboard_manage_board")) {
		t.Fatal("the read loop must keep running")
	}
	s.handleServerMessage(realtimeToolCallEvent("call_2", "whiteboard_write_content"))
	if events := drainRealtimeEvents(t, s.Events()); len(events) != 0 {
		t.Fatalf("tool calls must wait for response.done: %+v", events)
	}

	s.handleServerMessage(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
			Output: []realtime.ConversationItemUnion{{Type: "function_call"}},
		}},
	})

	events := drainRealtimeEvents(t, s.Events())
	if len(events) != 2 || events[0].CallID != "call_1" || events[1].CallID != "call_2" {
		t.Fatalf("both tool calls of the response must be released together: %+v", events)
	}

	// The first tool finished while the second one still runs.
	if err := s.SendToolResult("call_1", "whiteboard_manage_board", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	sent := conn.sentEvents()
	if len(sent) != 1 {
		t.Fatalf("an unanswered sibling call must withhold response.create: %+v", sent)
	}
	if callID := sent[0].OfConversationItemCreate.Item.OfFunctionCallOutput.CallID; callID != "call_1" {
		t.Fatalf("unexpected function call output: %+v", sent[0])
	}

	// Whiteboard context that arrives while the second tool runs waits too.
	if err := s.SendContextText("user", "the board now shows x=42", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err := s.SendImage("image/webp", []byte("img"), "the board"); err != nil {
		t.Fatalf("SendImage: %v", err)
	}
	if sent = conn.sentEvents(); len(sent) != 1 {
		t.Fatalf("context must wait for the pending tool output: %+v", sent)
	}

	if err := s.SendToolResult("call_2", "whiteboard_write_content", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	sent = conn.sentEvents()
	if len(sent) != 5 {
		t.Fatalf("expected the second output, both deferred events and response.create: %+v", sent)
	}
	if callID := sent[1].OfConversationItemCreate.Item.OfFunctionCallOutput.CallID; callID != "call_2" {
		t.Fatalf("unexpected second function call output: %+v", sent[1])
	}
	if sent[2].OfConversationItemCreate == nil || sent[3].OfConversationItemCreate == nil {
		t.Fatalf("deferred context must follow the last output: %+v", sent)
	}
	if sent[4].OfResponseCreate == nil {
		t.Fatalf("response.create must stay last: %+v", sent[4])
	}
}

// Deferred context that asked for a response already carries its own
// response.create, so the tool result must not add a second one.
func TestRealtimeAgentSessionDeferredContextTriggersResponseOnce(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	s.handleServerMessage(realtimeToolCallEvent("call_1", "whiteboard_capture_snapshot"))
	s.handleServerMessage(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
			Output: []realtime.ConversationItemUnion{{Type: "function_call"}},
		}},
	})
	drainRealtimeEvents(t, s.Events())

	if err := s.SendContextText("user", "read the board out loud", true); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	if err := s.SendToolResult("call_1", "whiteboard_capture_snapshot", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}

	sent := conn.sentEvents()
	if len(sent) != 3 {
		t.Fatalf("expected the output, the deferred item and its response.create: %+v", sent)
	}
	if sent[1].OfConversationItemCreate == nil || sent[2].OfResponseCreate == nil {
		t.Fatalf("unexpected order: %+v", sent)
	}
}

// A failed response abandons its tool turn, so a later tool result must not be
// blocked by calls that can never be answered.
func TestRealtimeAgentSessionFailedResponseResetsToolTurn(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	s.handleServerMessage(realtimeToolCallEvent("call_1", "whiteboard_manage_board"))
	s.handleServerMessage(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
			Output: []realtime.ConversationItemUnion{{Type: "function_call"}},
		}},
	})
	drainRealtimeEvents(t, s.Events())

	if err := s.SendContextText("user", "the board is open", false); err != nil {
		t.Fatalf("SendContextText: %v", err)
	}
	s.handleServerMessage(realtime.RealtimeServerEventUnion{
		Type: "response.done",
		OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusFailed,
		}},
	})
	drainRealtimeEvents(t, s.Events())

	if err := s.SendToolResult("call_1", "whiteboard_manage_board", `{"ok":true}`, false); err != nil {
		t.Fatalf("SendToolResult: %v", err)
	}
	events := conn.sentEvents()
	if len(events) != 2 || events[1].OfResponseCreate == nil {
		t.Fatalf("a dropped tool turn must not block the next result: %+v", events)
	}
}

func TestRealtimeAgentSessionInterrupt(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.Interrupt(); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	events := conn.sentEvents()
	if len(events) != 2 || events[0].OfResponseCancel == nil || events[1].OfOutputAudioBufferClear == nil {
		t.Fatalf("unexpected interrupt events: %+v", events)
	}
}

func TestRealtimeAgentSessionSendAfterClose(t *testing.T) {
	conn := &fakeRealtimeAgentConn{}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.SendAudio([]byte{1, 2}); !errors.Is(err, errRealtimeAgentSessionClosed) {
		t.Fatalf("expected errRealtimeAgentSessionClosed, got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
}

func TestRealtimeAgentSessionSendError(t *testing.T) {
	conn := &fakeRealtimeAgentConn{writeFail: errors.New("socket is broken")}
	s := newRealtimeAgentSession(conn, nil)

	if err := s.SendAudio([]byte{1, 2}); err == nil {
		t.Fatal("send errors must be reported")
	}
}

func TestRealtimeAgentSessionReadLoop(t *testing.T) {
	conn := &fakeRealtimeAgentConn{incoming: []realtime.RealtimeServerEventUnion{
		{
			Type: "response.output_audio.delta",
			OfResponseAudioDeltaEvent: realtime.ResponseAudioDeltaEvent{
				Delta: base64.StdEncoding.EncodeToString([]byte{9, 9}),
			},
		},
		{Type: "response.done", OfResponseDoneEvent: realtime.ResponseDoneEvent{Response: realtime.RealtimeResponse{
			Status: realtime.RealtimeResponseStatusCompleted,
		}}},
	}}
	s := newRealtimeAgentSession(conn, nil)
	s.wg.Add(1)
	go s.readLoop()

	var events []insights.RealtimeEvent
	for event := range s.Events() {
		events = append(events, event)
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

// A goroutine that emits after the read loop closed the stream must not panic.
func TestRealtimeAgentSessionEmitAfterClose(t *testing.T) {
	conn := &fakeRealtimeAgentConn{block: make(chan struct{})}
	s := newRealtimeAgentSession(conn, nil)
	s.wg.Add(1)
	go s.readLoop()

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for range s.Events() {
	}

	if s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete}) {
		t.Fatal("emit after close must report the closed stream")
	}
}

func TestRealtimeAgentSessionMaxDurationEmitsGoAway(t *testing.T) {
	conn := &fakeRealtimeAgentConn{block: make(chan struct{})}
	s := newRealtimeAgentSession(conn, nil)
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
