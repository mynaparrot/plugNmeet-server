package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// translateServerMessage maps one provider message onto zero or more events.
func translateServerMessage(msg *genai.LiveServerMessage) []insights.RealtimeEvent {
	if msg == nil {
		return nil
	}

	var events []insights.RealtimeEvent

	if sc := msg.ServerContent; sc != nil {
		if sc.Interrupted {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})
		}
		// Interim input transcription arrives before the final one; both are forwarded.
		if t := sc.InterimInputTranscription; t != nil && t.Text != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: t.Text})
		}
		if t := sc.InputTranscription; t != nil && t.Text != "" {
			events = append(events, insights.RealtimeEvent{
				Type:  insights.RealtimeEventTranscriptUser,
				Text:  t.Text,
				Final: t.Finished,
			})
		}
		if t := sc.OutputTranscription; t != nil && t.Text != "" {
			events = append(events, insights.RealtimeEvent{
				Type:  insights.RealtimeEventTranscriptAgent,
				Text:  t.Text,
				Final: t.Finished,
			})
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
		if sc.TurnComplete {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}
	}

	if msg.ToolCall != nil {
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

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup
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

func (s *liveSession) SendContextText(role, text string, triggerResponse bool) error {
	if text == "" {
		return nil
	}
	if role == "" {
		role = genai.RoleUser
	}
	return s.send(func() error {
		return s.conn.SendClientContent(genai.LiveClientContentInput{
			Turns:        []*genai.Content{{Role: role, Parts: []*genai.Part{{Text: text}}}},
			TurnComplete: genai.Ptr(triggerResponse),
		})
	})
}

func (s *liveSession) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	if toolName == "" {
		return errors.New("google: tool result requires a tool name")
	}
	return s.send(func() error {
		return s.conn.SendToolResponse(genai.LiveToolResponseInput{
			FunctionResponses: []*genai.FunctionResponse{{
				ID:       callID,
				Name:     toolName,
				Response: functionResponsePayload(resultJSON, isError),
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

func (s *liveSession) emit(ev insights.RealtimeEvent) bool {
	select {
	case s.events <- ev:
		return true
	case <-s.done:
		return false
	}
}

func (s *liveSession) readLoop() {
	defer s.wg.Done()
	defer close(s.events)
	defer func() {
		if err := s.shutdown(); err != nil && s.logger != nil {
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
		for _, ev := range translateServerMessage(msg) {
			if !s.emit(ev) {
				return
			}
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
