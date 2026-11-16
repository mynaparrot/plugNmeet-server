package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/realtime"
	"github.com/sirupsen/logrus"
)

const (
	defaultRealtimeAgentModel = string(realtime.RealtimeSessionCreateRequestModelGPTRealtime2_1Mini)
	defaultRealtimeAgentVoice = "marin"
	// defaultRealtimeInputTranscriptionModel is gpt-transcribe because gpt-live-transcribe
	// rejects turn detection with server_vad/semantic_vad.
	defaultRealtimeInputTranscriptionModel = string(realtime.AudioTranscriptionModelGPTTranscribe)
	realtimeEventBuffer                    = 256

	minRealtimeSpeakingRate = 0.25
	maxRealtimeSpeakingRate = 1.5

	realtimeContentTypeText  = "input_text"
	realtimeContentTypeImage = "input_image"
	realtimeAudioFormatPCM   = "audio/pcm"
)

var errRealtimeAgentSessionClosed = errors.New("openai: realtime session is closed")

// realtimeAgentConn is the subset of *realtime.RealtimeConnection used here, so
// the session stays testable without a socket.
type realtimeAgentConn interface {
	Send(ctx context.Context, event realtime.RealtimeClientEventUnionParam) error
	Recv(ctx context.Context) (realtime.RealtimeServerEventUnion, error)
	Close() error
}

// createRealtimeAgentSession opens an OpenAI Realtime session. It works with
// OpenAI models and with OpenAI-compatible self-hosted servers.
func (p *OpenAIProvider) createRealtimeAgentSession(ctx context.Context, model string, opts *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	turnDetectionMode := p.realtimeOption(opts, "turn_detection", transcriptionTurnDetectionSemanticVAD)
	transcriptionModel := p.realtimeOption(opts, "input_transcription_model", defaultRealtimeInputTranscriptionModel)

	voice := opts.Voice
	if voice == "" {
		voice = p.serviceOption("default_voice", defaultRealtimeAgentVoice)
	}

	connectOpts := append(p.realtimeRequestOptions(), option.WithQuery("model", model))
	conn, err := p.openAiClient.Realtime.Connect(ctx, realtime.RealtimeConnectionOptions{}, connectOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to openai realtime session: %w", err)
	}

	sessionUpdate := realtime.RealtimeClientEventParamOfSessionUpdate(
		buildRealtimeSessionConfig(model, opts, turnDetectionMode, transcriptionModel, voice),
	)
	if err = conn.Send(ctx, sessionUpdate); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to configure openai realtime session: %w", err)
	}

	s := newRealtimeAgentSession(conn, p.logger)
	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(ctx, opts.MaxSessionDuration)

	return s, nil
}

// buildRealtimeSessionConfig maps provider-agnostic options onto a session.update.
func buildRealtimeSessionConfig(model string, opts *insights.RealtimeSessionOptions, turnDetectionMode, transcriptionModel, voice string) realtime.RealtimeSessionCreateRequestParam {
	cfg := realtime.RealtimeSessionCreateRequestParam{
		Model:            realtime.RealtimeSessionCreateRequestModel(model),
		OutputModalities: []string{"audio"},
		Audio: realtime.RealtimeAudioConfigParam{
			Input: realtime.RealtimeAudioConfigInputParam{
				Format:        realtimePCMFormat(),
				TurnDetection: realtimeTurnDetection(turnDetectionMode),
			},
			Output: realtime.RealtimeAudioConfigOutputParam{
				Format: realtimePCMFormat(),
				Speed:  realtimeSpeakingRate(opts.SpeakingRate),
			},
		},
	}

	if opts.SystemInstruction != "" {
		cfg.Instructions = param.NewOpt(opts.SystemInstruction)
	}

	if realtimeTranscriptionEnabled(transcriptionModel) {
		transcription := realtime.AudioTranscriptionParam{
			Model: realtime.AudioTranscriptionModel(transcriptionModel),
		}
		if lang := normalizeiso6391(opts.SpokenLang); lang != "" {
			transcription.Language = param.NewOpt(lang)
		}
		cfg.Audio.Input.Transcription = transcription
	}

	if voice != "" {
		cfg.Audio.Output.Voice = realtime.RealtimeAudioConfigOutputVoiceUnionParam{OfString: param.NewOpt(voice)}
	}

	if tools := buildRealtimeTools(opts.Tools); len(tools) > 0 {
		cfg.Tools = tools
	}

	return cfg
}

func realtimePCMFormat() realtime.RealtimeAudioFormatsUnionParam {
	return realtime.RealtimeAudioFormatsUnionParam{OfAudioPCM: &realtime.RealtimeAudioFormatsAudioPCMParam{
		Rate: defaultRealtimeSampleRate,
		Type: realtimeAudioFormatPCM,
	}}
}

// realtimeTurnDetection returns an empty union (eldided) when the model must be
// driven manually, which is what "none"/"manual"/"null" ask for.
func realtimeTurnDetection(mode string) realtime.RealtimeAudioInputTurnDetectionUnionParam {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case transcriptionTurnDetectionServerVAD:
		return realtime.RealtimeAudioInputTurnDetectionUnionParam{OfServerVad: &realtime.RealtimeAudioInputTurnDetectionServerVadParam{
			CreateResponse:    param.NewOpt(true),
			InterruptResponse: param.NewOpt(true),
		}}
	case "", transcriptionTurnDetectionSemanticVAD:
		return realtime.RealtimeAudioInputTurnDetectionUnionParam{OfSemanticVad: &realtime.RealtimeAudioInputTurnDetectionSemanticVadParam{
			Eagerness:         "auto",
			CreateResponse:    param.NewOpt(true),
			InterruptResponse: param.NewOpt(true),
		}}
	default:
		return realtime.RealtimeAudioInputTurnDetectionUnionParam{}
	}
}

func realtimeTranscriptionEnabled(model string) bool {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", transcriptionTurnDetectionNone, transcriptionTurnDetectionDisabled, "off":
		return false
	default:
		return true
	}
}

func realtimeSpeakingRate(rate float32) param.Opt[float64] {
	if rate <= 0 {
		return param.Opt[float64]{}
	}

	speed := float64(rate)
	if speed < minRealtimeSpeakingRate {
		speed = minRealtimeSpeakingRate
	}
	if speed > maxRealtimeSpeakingRate {
		speed = maxRealtimeSpeakingRate
	}

	return param.NewOpt(speed)
}

func buildRealtimeTools(tools []*insights.MCPToolSpec) []realtime.RealtimeToolsConfigUnionParam {
	params := make([]realtime.RealtimeToolsConfigUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Name == "" {
			continue
		}
		function := realtime.RealtimeFunctionToolParam{
			Name:        param.NewOpt(tool.Name),
			Description: param.NewOpt(tool.Description),
			Parameters:  tool.InputSchema,
			Type:        realtime.RealtimeFunctionToolTypeFunction,
		}
		params = append(params, realtime.RealtimeToolsConfigUnionParam{OfFunction: &function})
	}
	return params
}

// normalizeiso6391 turns a locale such as "en-US" into the ISO-639-1 code the
// transcription models expect.
func normalizeiso6391(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i > 0 {
		return tag[:i]
	}
	return tag
}

// formatToolOutputJSON normalizes a tool result into the JSON string providers
// expect on a function_call_output item.
func formatToolOutputJSON(resultJSON string, isError bool) string {
	const fallback = `{"error":"tool call failed"}`

	if !isError {
		if strings.TrimSpace(resultJSON) == "" {
			return "{}"
		}
		return resultJSON
	}

	if strings.TrimSpace(resultJSON) == "" {
		return fallback
	}

	payload, err := json.Marshal(map[string]string{"error": resultJSON})
	if err != nil {
		return fallback
	}
	return string(payload)
}

func defaultArgsJSON(args string) string {
	if strings.TrimSpace(args) == "" {
		return "{}"
	}
	return args
}

// translateRealtimeServerEvent maps one server event onto zero or more events.
func translateRealtimeServerEvent(msg realtime.RealtimeServerEventUnion) []insights.RealtimeEvent {
	var events []insights.RealtimeEvent

	switch msg.Type {
	case "input_audio_buffer.speech_started":
		events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})

	case "conversation.item.input_audio_transcription.delta":
		if delta := msg.OfConversationItemInputAudioTranscriptionDeltaEvent.Delta; delta != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: delta})
		}

	case "conversation.item.input_audio_transcription.completed":
		if transcript := msg.OfConversationItemInputAudioTranscriptionCompletedEvent.Transcript; transcript != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: transcript, Final: true})
		}

	case "response.output_audio_transcript.delta":
		if delta := msg.OfResponseAudioTranscriptDeltaEvent.Delta; delta != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: delta})
		}

	case "response.output_audio_transcript.done":
		if transcript := msg.OfResponseAudioTranscriptDoneEvent.Transcript; transcript != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: transcript, Final: true})
		}

	case "response.output_audio.delta":
		pcm, err := base64.StdEncoding.DecodeString(msg.OfResponseAudioDeltaEvent.Delta)
		if err == nil && len(pcm) > 0 {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: pcm})
		}

	case "response.function_call_arguments.done":
		ev := msg.OfResponseFunctionCallArgumentsDoneEvent
		events = append(events, insights.RealtimeEvent{
			Type:     insights.RealtimeEventToolCall,
			CallID:   ev.CallID,
			ToolName: ev.Name,
			ArgsJSON: defaultArgsJSON(ev.Arguments),
		})

	case "response.done":
		events = append(events, translateRealtimeResponseDone(msg.OfResponseDoneEvent)...)

	case "error":
		ev := msg.OfRealtimeErrorEvent
		events = append(events, insights.RealtimeEvent{
			Type: insights.RealtimeEventError,
			Err:  fmt.Errorf("openai realtime: %s (%s)", ev.Error.Message, ev.Error.Code),
		})
	}

	return events
}

func translateRealtimeResponseDone(ev realtime.ResponseDoneEvent) []insights.RealtimeEvent {
	var events []insights.RealtimeEvent

	if usage := ev.Response.Usage; usage.InputTokens > 0 || usage.OutputTokens > 0 {
		events = append(events, insights.RealtimeEvent{
			Type:         insights.RealtimeEventUsage,
			InputTokens:  realtimeTokenCount(usage.InputTokens),
			OutputTokens: realtimeTokenCount(usage.OutputTokens),
		})
	}

	// A function call turn is answered by the caller via SendToolResult, so the
	// turn must not be reported as complete yet.
	functionCall := false
	for _, item := range ev.Response.Output {
		if item.Type == "function_call" {
			functionCall = true
			break
		}
	}

	switch ev.Response.Status {
	case realtime.RealtimeResponseStatusCompleted:
		if !functionCall {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}
	case realtime.RealtimeResponseStatusFailed:
		events = append(events, insights.RealtimeEvent{
			Type: insights.RealtimeEventError,
			Err:  errors.New(realtimeResponseFailureMessage(ev.Response)),
		})
	}

	return events
}

// realtimeResponseFailureMessage reads the error message from status_details,
// which the SDK exposes as typed JSON only.
func realtimeResponseFailureMessage(resp realtime.RealtimeResponse) string {
	if raw := resp.JSON.StatusDetails; raw.Valid() {
		var details struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw.Raw()), &details); err == nil && details.Error.Message != "" {
			return details.Error.Message
		}
	}

	if status := string(resp.StatusDetails); status != "" {
		return status
	}

	return "response failed"
}

func realtimeTokenCount(v int64) uint32 {
	if v <= 0 {
		return 0
	}
	return uint32(v)
}

// realtimeAgentSession implements insights.RealtimeSession on top of a Realtime socket.
type realtimeAgentSession struct {
	conn   realtimeAgentConn
	logger *logrus.Entry

	events chan insights.RealtimeEvent
	done   chan struct{}

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newRealtimeAgentSession(conn realtimeAgentConn, logger *logrus.Entry) *realtimeAgentSession {
	return &realtimeAgentSession{
		conn:   conn,
		logger: logger,
		events: make(chan insights.RealtimeEvent, realtimeEventBuffer),
		done:   make(chan struct{}),
	}
}

func (s *realtimeAgentSession) Events() <-chan insights.RealtimeEvent {
	return s.events
}

func (s *realtimeAgentSession) SendAudio(pcm16kMono []byte) error {
	if len(pcm16kMono) == 0 {
		return nil
	}

	pcm := resamplePCM16Bytes16kTo24k(pcm16kMono)
	if len(pcm) == 0 {
		return nil
	}

	return s.send(realtime.RealtimeClientEventParamOfInputAudioBufferAppend(base64.StdEncoding.EncodeToString(pcm)))
}

func (s *realtimeAgentSession) SendContextText(role, text string, triggerResponse bool) error {
	if text == "" {
		return nil
	}

	var create realtime.RealtimeClientEventUnionParam
	if !triggerResponse || role == "system" || role == "developer" {
		// Context the model must not mistake for participant speech.
		create = realtime.RealtimeClientEventParamOfConversationItemCreate(realtime.RealtimeConversationItemSystemMessageParam{
			Content: []realtime.RealtimeConversationItemSystemMessageContentParam{{
				Type: realtimeContentTypeText,
				Text: param.NewOpt(text),
			}},
		})
	} else {
		create = realtime.RealtimeClientEventParamOfConversationItemCreate(realtime.RealtimeConversationItemUserMessageParam{
			Content: []realtime.RealtimeConversationItemUserMessageContentParam{{
				Type: realtimeContentTypeText,
				Text: param.NewOpt(text),
			}},
		})
	}

	events := []realtime.RealtimeClientEventUnionParam{create}
	if triggerResponse {
		events = append(events, realtime.RealtimeClientEventUnionParam{OfResponseCreate: &realtime.ResponseCreateEventParam{}})
	}

	return s.send(events...)
}

func (s *realtimeAgentSession) SendImage(mimeType string, data []byte, caption string) error {
	if len(data) == 0 {
		return nil
	}
	if mimeType == "" {
		mimeType = "image/webp"
	}

	content := make([]realtime.RealtimeConversationItemUserMessageContentParam, 0, 2)
	content = append(content, realtime.RealtimeConversationItemUserMessageContentParam{
		Type:     realtimeContentTypeImage,
		ImageURL: param.NewOpt(fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data))),
		Detail:   "auto",
	})
	if caption != "" {
		content = append(content, realtime.RealtimeConversationItemUserMessageContentParam{
			Type: realtimeContentTypeText,
			Text: param.NewOpt(caption),
		})
	}

	return s.send(realtime.RealtimeClientEventParamOfConversationItemCreate(
		realtime.RealtimeConversationItemUserMessageParam{Content: content},
	))
}

func (s *realtimeAgentSession) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	output := formatToolOutputJSON(resultJSON, isError)

	return s.send(
		realtime.RealtimeClientEventParamOfConversationItemCreate(realtime.RealtimeConversationItemFunctionCallOutputParam{
			CallID: callID,
			Output: output,
		}),
		realtime.RealtimeClientEventUnionParam{OfResponseCreate: &realtime.ResponseCreateEventParam{}},
	)
}

func (s *realtimeAgentSession) Interrupt() error {
	return s.send(
		realtime.RealtimeClientEventUnionParam{OfResponseCancel: &realtime.ResponseCancelEventParam{}},
		realtime.RealtimeClientEventUnionParam{OfOutputAudioBufferClear: &realtime.OutputAudioBufferClearEventParam{}},
	)
}

func (s *realtimeAgentSession) Close() error {
	err := s.shutdown()
	s.wg.Wait()
	return err
}

func (s *realtimeAgentSession) send(events ...realtime.RealtimeClientEventUnionParam) error {
	if s.isClosed() {
		return errRealtimeAgentSessionClosed
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	for _, ev := range events {
		if err := s.conn.Send(context.Background(), ev); err != nil {
			return fmt.Errorf("openai: failed to send realtime event: %w", err)
		}
	}
	return nil
}

func (s *realtimeAgentSession) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// shutdown closes the socket exactly once. The read loop owns the events
// channel and closes it when it returns, so Close() waits for it.
func (s *realtimeAgentSession) shutdown() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.conn.Close()
	})
	return err
}

func (s *realtimeAgentSession) emit(ev insights.RealtimeEvent) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.done:
		return false
	}
}

func (s *realtimeAgentSession) readLoop() {
	defer s.wg.Done()
	defer close(s.events)
	defer func() {
		if err := s.shutdown(); err != nil && s.logger != nil {
			s.logger.WithError(err).Debug("openai realtime session close returned an error")
		}
	}()

	for {
		msg, err := s.conn.Recv(context.Background())
		if err != nil {
			if !s.isClosed() {
				s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventError, Err: err})
			}
			return
		}
		for _, ev := range translateRealtimeServerEvent(msg) {
			if !s.emit(ev) {
				return
			}
		}
	}
}

// supervise closes the session on context cancellation or when the configured
// maximum socket lifetime is reached. The caller is expected to re-dial.
func (s *realtimeAgentSession) supervise(ctx context.Context, maxDuration time.Duration) {
	var maxLifetime <-chan time.Time
	if maxDuration > 0 {
		timer := time.NewTimer(maxDuration)
		defer timer.Stop()
		maxLifetime = timer.C
	}

	select {
	case <-s.done:
	case <-ctx.Done():
		_ = s.shutdown()
	case <-maxLifetime:
		s.emit(insights.RealtimeEvent{
			Type: insights.RealtimeEventGoAway,
			Text: "max session duration reached",
		})
		_ = s.shutdown()
	}
}
