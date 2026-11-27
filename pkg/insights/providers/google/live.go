package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

const (
	// defaultLiveModel is used when the caller does not name a model.
	defaultLiveModel = "gemini-3.8-live"
	// liveAudioMIMEType is Gemini's native realtime input format: 16 kHz PCM16 mono.
	liveAudioMIMEType = "audio/pcm;rate=16000"
	liveEventBuffer   = 256
)

var errLiveSessionClosed = errors.New("google: live session is closed")

// liveConn is the subset of *genai.Session used by liveSession.
// Keeping it narrow makes the session testable without a socket.
type liveConn interface {
	SendRealtimeInput(input genai.LiveRealtimeInput) error
	SendClientContent(input genai.LiveClientContentInput) error
	SendToolResponse(input genai.LiveToolResponseInput) error
	Receive() (*genai.LiveServerMessage, error)
	Close() error
}

// CreateRealtimeSession opens a Gemini Live session for a multimodal agent.
func (p *GoogleProvider) CreateRealtimeSession(ctx context.Context, model string, opts *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	if opts == nil {
		opts = &insights.RealtimeSessionOptions{}
	}
	if model == "" {
		model = defaultLiveModel
	}

	conn, err := p.client.Live.Connect(ctx, model, buildLiveConnectConfig(opts))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gemini live session: %w", err)
	}

	s := newLiveSession(conn, p.logger)
	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(ctx, opts.MaxSessionDuration)

	return s, nil
}

// buildLiveConnectConfig maps provider-agnostic options onto the Live setup.
// SpeakingRate/TargetLang/InstructionLang have no Live API counterpart: the
// caller folds them into SystemInstruction instead.
func buildLiveConnectConfig(opts *insights.RealtimeSessionOptions) *genai.LiveConnectConfig {
	cfg := &genai.LiveConnectConfig{
		ResponseModalities:       []genai.Modality{genai.ModalityAudio},
		InputAudioTranscription:  &genai.AudioTranscriptionConfig{},
		OutputAudioTranscription: &genai.AudioTranscriptionConfig{},
		// Low start sensitivity plus the prefix padding delay keeps a keyboard
		// click or a tap from looking like the start of a user turn, which would
		// cut off the model's own audio mid-sentence.
		RealtimeInputConfig: &genai.RealtimeInputConfig{
			AutomaticActivityDetection: &genai.AutomaticActivityDetection{
				StartOfSpeechSensitivity: genai.StartSensitivityLow,
				PrefixPaddingMs:          genai.Ptr(int32(300)),
				SilenceDurationMs:        genai.Ptr(int32(500)),
			},
		},
	}

	if opts.SystemInstruction != "" {
		cfg.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: opts.SystemInstruction}},
		}
	}

	if opts.Voice != "" || opts.SpokenLang != "" {
		speech := &genai.SpeechConfig{LanguageCode: opts.SpokenLang}
		if opts.Voice != "" {
			speech.VoiceConfig = &genai.VoiceConfig{
				PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: opts.Voice},
			}
		}
		cfg.SpeechConfig = speech
	}

	if decls := buildFunctionDeclarations(opts.Tools); len(decls) > 0 {
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	}

	return cfg
}

// buildFunctionDeclarations converts MCP shaped tool specs into Gemini declarations.
func buildFunctionDeclarations(tools []*insights.MCPToolSpec) []*genai.FunctionDeclaration {
	decls := make([]*genai.FunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Name == "" {
			continue
		}
		decl := &genai.FunctionDeclaration{
			Name:        tool.Name,
			Description: tool.Description,
		}
		if len(tool.InputSchema) > 0 {
			decl.ParametersJsonSchema = tool.InputSchema
		}
		decls = append(decls, decl)
	}
	return decls
}

// functionResponsePayload shapes a tool result the way Gemini expects:
// the "output" key for results, the "error" key for failures.
func functionResponsePayload(resultJSON string, isError bool) map[string]any {
	key := "output"
	if isError {
		key = "error"
	}

	var value any = resultJSON
	if resultJSON != "" {
		var parsed any
		if err := json.Unmarshal([]byte(resultJSON), &parsed); err == nil {
			value = parsed
		}
	}

	return map[string]any{key: value}
}

// translateServerMessage maps one provider message onto zero or more events
// without keeping any streaming state: a transcription that already carries
// Finished becomes its own final event.
func translateServerMessage(msg *genai.LiveServerMessage) []insights.RealtimeEvent {
	var tr liveTranscripts
	return translateServerMessageWithState(msg, &tr)
}

// liveTranscripts accumulates the streamed transcription fragments of the
// current turn. Gemini streams both speakers as fragments with Finished unset
// and only reports the end of the turn, so the fragments are joined here.
type liveTranscripts struct {
	input  transcriptBuffer
	output transcriptBuffer
}

// transcriptBuffer collects the transcription fragments of one speaker.
type transcriptBuffer struct{ strings.Builder }

// add records one fragment and returns the event to emit: the fragment itself
// while the stream continues, or the whole utterance once the provider marks it
// done.
func (b *transcriptBuffer) add(eventType insights.RealtimeEventType, text string, finished bool) insights.RealtimeEvent {
	if !finished {
		b.WriteString(text)
		return insights.RealtimeEvent{Type: eventType, Text: text}
	}
	if b.Len() > 0 {
		b.WriteString(text)
		text = b.String()
	}
	b.Reset()
	return insights.RealtimeEvent{Type: eventType, Text: text, Final: true}
}

// flush returns the collected utterance as a final event, if there is one.
func (b *transcriptBuffer) flush(eventType insights.RealtimeEventType) (insights.RealtimeEvent, bool) {
	if b.Len() == 0 {
		return insights.RealtimeEvent{}, false
	}
	ev := insights.RealtimeEvent{Type: eventType, Text: b.String(), Final: true}
	b.Reset()
	return ev, true
}

// translateServerMessageWithState maps one message onto events, ending the
// streamed utterance of a speaker before the message that closes its turn.
func translateServerMessageWithState(msg *genai.LiveServerMessage, tr *liveTranscripts) []insights.RealtimeEvent {
	if msg == nil {
		return nil
	}

	var events []insights.RealtimeEvent
	endUserUtterance := func() {
		if ev, ok := tr.input.flush(insights.RealtimeEventTranscriptUser); ok {
			events = append(events, ev)
		}
	}
	endAgentUtterance := func() {
		if ev, ok := tr.output.flush(insights.RealtimeEventTranscriptAgent); ok {
			events = append(events, ev)
		}
	}

	if sc := msg.ServerContent; sc != nil {
		if sc.Interrupted {
			// The participant took the turn back, so both utterances end here.
			endUserUtterance()
			endAgentUtterance()
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})
		}
		// Interim input transcription arrives before the final one; both are forwarded.
		if t := sc.InterimInputTranscription; t != nil && t.Text != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: t.Text})
		}
		if t := sc.InputTranscription; t != nil && t.Text != "" {
			events = append(events, tr.input.add(insights.RealtimeEventTranscriptUser, t.Text, t.Finished))
		}
		// The model only replies once the participant stopped speaking.
		if sc.ModelTurn != nil || (sc.OutputTranscription != nil && sc.OutputTranscription.Text != "") {
			endUserUtterance()
		}
		if t := sc.OutputTranscription; t != nil && t.Text != "" {
			events = append(events, tr.output.add(insights.RealtimeEventTranscriptAgent, t.Text, t.Finished))
		}
		if sc.ModelTurn != nil {
			for _, part := range sc.ModelTurn.Parts {
				if part == nil || part.InlineData == nil || len(part.InlineData.Data) == 0 {
					continue
				}
				audio := make([]byte, len(part.InlineData.Data))
				copy(audio, part.InlineData.Data)
				events = append(events, insights.RealtimeEvent{
					Type:        insights.RealtimeEventAudioDelta,
					AudioPCM24k: audio,
				})
			}
		}
		if sc.TurnComplete || sc.GenerationComplete {
			// The agent transcript must reach the consumer before the turn ends.
			endUserUtterance()
			endAgentUtterance()
		}
		if sc.TurnComplete {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}
	}

	if msg.ToolCall != nil {
		// A tool call answers the participant: the pending speech ends here.
		endUserUtterance()
		for _, fc := range msg.ToolCall.FunctionCalls {
			if fc == nil {
				continue
			}
			args := "{}"
			if len(fc.Args) > 0 {
				if b, err := json.Marshal(fc.Args); err == nil {
					args = string(b)
				}
			}
			events = append(events, insights.RealtimeEvent{
				Type:     insights.RealtimeEventToolCall,
				CallID:   fc.ID,
				ToolName: fc.Name,
				ArgsJSON: args,
			})
		}
	}

	if um := msg.UsageMetadata; um != nil {
		events = append(events, translateUsageMetadata(um))
	}

	if msg.GoAway != nil {
		events = append(events, insights.RealtimeEvent{
			Type: insights.RealtimeEventGoAway,
			Text: fmt.Sprintf("provider closes the session in %s", msg.GoAway.TimeLeft),
		})
	}

	return events
}

// translateMessage maps one provider message onto events, carrying the streamed
// transcription fragments across messages so every turn ends with one final
// transcript per speaker.
func (s *liveSession) translateMessage(msg *genai.LiveServerMessage) []insights.RealtimeEvent {
	return translateServerMessageWithState(msg, &s.transcripts)
}

// translateUsageMetadata turns one usage report into the provider-agnostic
// event. Gemini reports tool results and thinking tokens beside the prompt and
// the response, while billing them as input and as output.
func translateUsageMetadata(um *genai.UsageMetadata) insights.RealtimeEvent {
	thinking := tokenCount(um.ThoughtsTokenCount)
	ev := insights.RealtimeEvent{
		Type:                 insights.RealtimeEventUsage,
		InputTokens:          tokenCount(um.PromptTokenCount) + tokenCount(um.ToolUsePromptTokenCount),
		OutputTokens:         tokenCount(um.ResponseTokenCount) + thinking,
		CachedInputTokens:    tokenCount(um.CachedContentTokenCount),
		ThinkingOutputTokens: thinking,
	}

	// The details name the media modalities of each count; whatever they leave
	// unattributed is text, which keeps the split equal to the totals.
	for _, details := range [][]*genai.ModalityTokenCount{um.PromptTokensDetails, um.ToolUsePromptTokensDetails} {
		for _, d := range details {
			if d == nil {
				continue
			}
			switch d.Modality {
			case genai.MediaModalityAudio:
				ev.AudioInputTokens += tokenCount(d.TokenCount)
			case genai.MediaModalityImage, genai.MediaModalityVideo:
				ev.ImageInputTokens += tokenCount(d.TokenCount)
			}
		}
	}
	for _, d := range um.CacheTokensDetails {
		if d == nil {
			continue
		}
		switch d.Modality {
		case genai.MediaModalityAudio:
			ev.CachedAudioInputTokens += tokenCount(d.TokenCount)
		case genai.MediaModalityImage, genai.MediaModalityVideo:
			ev.CachedImageInputTokens += tokenCount(d.TokenCount)
		}
	}
	for _, d := range um.ResponseTokensDetails {
		if d != nil && d.Modality == genai.MediaModalityAudio {
			ev.AudioOutputTokens += tokenCount(d.TokenCount)
		}
	}

	ev.AudioInputTokens = min(ev.AudioInputTokens, ev.InputTokens)
	ev.ImageInputTokens = min(ev.ImageInputTokens, ev.InputTokens-ev.AudioInputTokens)
	ev.TextInputTokens = ev.InputTokens - ev.AudioInputTokens - ev.ImageInputTokens

	ev.CachedAudioInputTokens = min(ev.CachedAudioInputTokens, ev.CachedInputTokens)
	ev.CachedImageInputTokens = min(ev.CachedImageInputTokens, ev.CachedInputTokens-ev.CachedAudioInputTokens)
	ev.CachedTextInputTokens = ev.CachedInputTokens - ev.CachedAudioInputTokens - ev.CachedImageInputTokens

	ev.AudioOutputTokens = min(ev.AudioOutputTokens, ev.OutputTokens)
	ev.TextOutputTokens = ev.OutputTokens - ev.AudioOutputTokens

	return ev
}

func tokenCount(v int32) uint32 {
	if v <= 0 {
		return 0
	}
	return uint32(v)
}

// liveSession implements insights.RealtimeSession on top of a Gemini Live socket.
type liveSession struct {
	conn   liveConn
	logger *logrus.Entry

	events chan insights.RealtimeEvent
	done   chan struct{}

	// emitMu guards eventsClosed, so a send can never race with the close.
	emitMu       sync.Mutex
	eventsClosed bool

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup

	// transcripts holds the transcription fragments of the running turn. Only
	// the read loop touches it.
	transcripts liveTranscripts
}

func newLiveSession(conn liveConn, logger *logrus.Entry) *liveSession {
	return &liveSession{
		conn:   conn,
		logger: logger,
		events: make(chan insights.RealtimeEvent, liveEventBuffer),
		done:   make(chan struct{}),
	}
}

func (s *liveSession) Events() <-chan insights.RealtimeEvent {
	return s.events
}

func (s *liveSession) SendAudio(pcm16kMono []byte) error {
	if len(pcm16kMono) == 0 {
		return nil
	}
	return s.send(func() error {
		return s.conn.SendRealtimeInput(genai.LiveRealtimeInput{
			Audio: &genai.Blob{MIMEType: liveAudioMIMEType, Data: pcm16kMono},
		})
	})
}

func (s *liveSession) SendImage(mimeType string, data []byte, caption string) error {
	if len(data) == 0 {
		return nil
	}
	if mimeType == "" {
		mimeType = "image/webp"
	}

	err := s.send(func() error {
		return s.conn.SendRealtimeInput(genai.LiveRealtimeInput{
			Video: &genai.Blob{MIMEType: mimeType, Data: data},
		})
	})
	if err != nil || caption == "" {
		return err
	}

	return s.SendContextText("user", caption, false)
}

// SendContextText injects server-side context. A context that must trigger an
// answer goes out as realtime input, on the same timeline as the microphone
// audio: as a clientContent turn it would stay in the history as a user turn that
// the model answers a second time on the next voice turn.
func (s *liveSession) SendContextText(role, text string, triggerResponse bool) error {
	if text == "" {
		return nil
	}
	if triggerResponse {
		return s.send(func() error {
			return s.conn.SendRealtimeInput(genai.LiveRealtimeInput{Text: text})
		})
	}
	if role == "" {
		role = genai.RoleUser
	}
	return s.send(func() error {
		return s.conn.SendClientContent(genai.LiveClientContentInput{
			Turns:        []*genai.Content{{Role: role, Parts: []*genai.Part{{Text: text}}}},
			TurnComplete: genai.Ptr(false),
		})
	})
}

// readOnlyTools return a payload the model needs in order to answer.
var readOnlyTools = map[string]struct{}{
	"notepad_read":                {},
	"whiteboard_capture_snapshot": {},
	"get_poll_results":            {},
	"get_room_info":               {},
	"fetch_external_knowledge":    {},
}

// toolResponseScheduling decides whether a tool result resumes generation. A
// read tool feeds the answer, so the model must speak after it. A write tool is
// narrated by the turn that called it, so its acknowledgement only belongs in
// the context and must not trigger a second spoken turn.
func toolResponseScheduling(toolName string, isError bool) genai.FunctionResponseScheduling {
	// A failure may need a retry or a spoken recovery.
	if isError {
		return genai.FunctionResponseSchedulingWhenIdle
	}
	if _, ok := readOnlyTools[toolName]; ok || strings.HasPrefix(toolName, "ext_") {
		return genai.FunctionResponseSchedulingWhenIdle
	}
	return genai.FunctionResponseSchedulingSilent
}

func (s *liveSession) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	if toolName == "" {
		return errors.New("google: tool result requires a tool name")
	}
	s.logger.WithFields(logrus.Fields{
		"callId":  callID,
		"tool":    toolName,
		"isError": isError,
	}).Debug("gemini live: sending tool result")
	return s.send(func() error {
		return s.conn.SendToolResponse(genai.LiveToolResponseInput{
			FunctionResponses: []*genai.FunctionResponse{{
				ID:         callID,
				Name:       toolName,
				Response:   functionResponsePayload(resultJSON, isError),
				Scheduling: toolResponseScheduling(toolName, isError),
			}},
		})
	})
}

// Interrupt is a no-op: Gemini detects barge-in server side and reports it as
// ServerContent.Interrupted. The caller drops queued audio locally.
func (s *liveSession) Interrupt() error {
	return nil
}

func (s *liveSession) Close() error {
	err := s.shutdown()
	s.wg.Wait()
	return err
}

func (s *liveSession) send(fn func() error) error {
	if s.isClosed() {
		return errLiveSessionClosed
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if err := fn(); err != nil {
		return fmt.Errorf("google: failed to send live message: %w", err)
	}
	return nil
}

func (s *liveSession) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// shutdown closes the socket exactly once. The read loop owns the events
// channel and closes it when it returns, so Close() waits for it.
func (s *liveSession) shutdown() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.conn.Close()
	})
	return err
}

// emit publishes one event. The close and the send share emitMu, so a goroutine
// that fires during shutdown can never write to a closed channel.
func (s *liveSession) emit(ev insights.RealtimeEvent) bool {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.eventsClosed {
		return false
	}

	select {
	case s.events <- ev:
		return true
	case <-s.done:
		return false
	}
}

// closeEvents ends the event stream. It shares emitMu with emit, so it cannot run
// concurrently with a send.
func (s *liveSession) closeEvents() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.eventsClosed {
		return
	}
	s.eventsClosed = true
	close(s.events)
}

func (s *liveSession) readLoop() {
	defer s.wg.Done()
	defer s.closeEvents()
	defer func() {
		if err := s.shutdown(); err != nil {
			s.logger.WithError(err).Debug("gemini live session close returned an error")
		}
	}()

	for {
		msg, err := s.conn.Receive()
		if err != nil {
			if !s.isClosed() {
				s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventError, Err: err})
			}
			return
		}
		s.logServerMessage(msg)
		for _, ev := range s.translateMessage(msg) {
			if !s.emit(ev) {
				return
			}
		}
	}
}

// logServerMessage records the control messages that decide how a turn ends.
func (s *liveSession) logServerMessage(msg *genai.LiveServerMessage) {
	if msg == nil {
		return
	}

	if tc := msg.ToolCall; tc != nil {
		s.logger.WithField("functionCalls", len(tc.FunctionCalls)).Debug("gemini live: tool call received")
		for _, fc := range tc.FunctionCalls {
			if fc == nil {
				continue
			}
			s.logger.WithFields(logrus.Fields{
				"callId": fc.ID,
				"tool":   fc.Name,
			}).Debug("gemini live: tool call function")
		}
	}

	if tc := msg.ToolCallCancellation; tc != nil {
		s.logger.WithField("ids", tc.IDs).Debug("gemini live: tool call cancelled")
	}

	if sc := msg.ServerContent; sc != nil {
		if sc.Interrupted {
			s.logger.Debug("gemini live: interrupted by the participant")
		}
		if sc.TurnComplete {
			s.logger.Debug("gemini live: turn complete")
		}
		if sc.GenerationComplete {
			s.logger.Debug("gemini live: generation complete")
		}
	}
}

// supervise closes the session on context cancellation or when the configured
// maximum socket lifetime is reached. The caller is expected to re-dial.
func (s *liveSession) supervise(ctx context.Context, maxDuration time.Duration) {
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
