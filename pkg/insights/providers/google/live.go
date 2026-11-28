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

	if decls := buildLiveFunctionDeclarations(opts.Tools); len(decls) > 0 {
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

// buildLiveFunctionDeclarations declares the tools for a live session. Write and
// action tools run in the browser while the model keeps talking, so they are
// non-blocking: a blocking declaration makes the model drop the rest of its turn
// and wait for the response, which leaves the user in silence. Read tools stay
// blocking because the model needs their payload before it can answer.
//
// The declaration also decides whether FunctionResponse.Scheduling is honoured:
// it only applies to non-blocking calls and is ignored (as WHEN_IDLE) otherwise.
//
// Behavior is only honoured by BidiGenerateContent, so the text chat path keeps
// using buildFunctionDeclarations.
func buildLiveFunctionDeclarations(tools []*insights.MCPToolSpec) []*genai.FunctionDeclaration {
	decls := buildFunctionDeclarations(tools)
	for _, decl := range decls {
		if !isReadOnlyTool(decl.Name) {
			decl.Behavior = genai.BehaviorNonBlocking
		}
	}
	return decls
}

// writeToolHint tells the model that the effect of a write tool is already on
// the participant's screen. Without it a bare {"ok":true} acknowledgement reads
// like "nothing happened yet", and the model calls the tool a second time.
const writeToolHint = "done: this action is already visible to the participant. Do not call this tool again with the same arguments; tell the participant what you did."

// functionResponsePayload shapes a tool result the way Gemini expects:
// the "output" key for results, the "error" key for failures.
func functionResponsePayload(toolName, resultJSON string, isError bool) map[string]any {
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

	payload := map[string]any{key: value}
	if !isError && !isReadOnlyTool(toolName) {
		payload["status"] = writeToolHint
	}
	return payload
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

	// thinking and responding track the current turn, so THINKING is reported
	// once and only while the model is still composing.
	thinking   bool
	responding bool
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
	emitThinking := func() {
		if tr.thinking || tr.responding {
			return
		}
		tr.thinking = true
		events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	}

	if sc := msg.ServerContent; sc != nil {
		if sc.Interrupted {
			// The participant took the turn back, so both utterances end here.
			endUserUtterance()
			endAgentUtterance()
			tr.thinking, tr.responding = false, false
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})
		}
		// Interim input transcription arrives before the final one; both are forwarded.
		if t := sc.InterimInputTranscription; t != nil && t.Text != "" {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: t.Text})
		}
		if t := sc.InputTranscription; t != nil && t.Text != "" {
			events = append(events, tr.input.add(insights.RealtimeEventTranscriptUser, t.Text, t.Finished))
			// Gemini closes the speech segment once the participant stops talking,
			// so this is where the reasoning gap starts.
			emitThinking()
		}
		// The model only replies once the participant stopped speaking.
		if sc.ModelTurn != nil || (sc.OutputTranscription != nil && sc.OutputTranscription.Text != "") {
			endUserUtterance()
		}
		if t := sc.OutputTranscription; t != nil && t.Text != "" {
			tr.responding = true
			events = append(events, tr.output.add(insights.RealtimeEventTranscriptAgent, t.Text, t.Finished))
		}
		if sc.ModelTurn != nil {
			for _, part := range sc.ModelTurn.Parts {
				if part == nil {
					continue
				}
				if part.InlineData == nil || len(part.InlineData.Data) == 0 {
					// Reasoning and text parts precede the audio they explain.
					if part.Thought || part.Text != "" {
						emitThinking()
					}
					continue
				}
				// Audio is the answer itself, so the turn stopped thinking.
				tr.responding = true
				audio := make([]byte, len(part.InlineData.Data))
				copy(audio, part.InlineData.Data)
				events = append(events, insights.RealtimeEvent{
					Type:        insights.RealtimeEventAudioDelta,
					AudioPCM24k: audio,
				})
			}
		}
		if sc.WaitingForInput {
			// The model chose to stay silent, so the pending utterance is closed
			// and the turn ended instead of leaving the state waiting.
			endUserUtterance()
			tr.thinking, tr.responding = false, false
			if !sc.TurnComplete {
				events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
			}
		}
		if sc.TurnComplete || sc.GenerationComplete {
			// The agent transcript must reach the consumer before the turn ends.
			endUserUtterance()
			endAgentUtterance()
			tr.thinking, tr.responding = false, false
		}
		if sc.TurnComplete {
			events = append(events, insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}
	}

	if msg.ToolCall != nil {
		// A tool call answers the participant: the pending speech ends here.
		endUserUtterance()
		tr.thinking, tr.responding = false, false
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
	if msg != nil && msg.ToolCallCancellation != nil {
		// A cancelled call never gets a result, so it must not hold the session
		// back as a pending call.
		s.forgetToolCalls(msg.ToolCallCancellation.IDs)
	}

	events := translateServerMessageWithState(msg, &s.transcripts)
	s.trackNarration(events)
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

	// toolMu guards the tool call bookkeeping, which the read loop writes and
	// the executor reads.
	toolMu sync.Mutex
	// pendingCalls holds the calls the model is still waiting on a result for.
	pendingCalls map[string]struct{}
	// turnCalls holds the calls issued by the turn being translated.
	turnCalls map[string]struct{}
	// unnarrated holds the calls whose turn produced no speech yet.
	unnarrated map[string]struct{}
	// turnNarrated reports whether the turn being translated already spoke.
	turnNarrated bool
}

func newLiveSession(conn liveConn, logger *logrus.Entry) *liveSession {
	return &liveSession{
		conn:         conn,
		logger:       logger,
		events:       make(chan insights.RealtimeEvent, liveEventBuffer),
		done:         make(chan struct{}),
		pendingCalls: make(map[string]struct{}),
		turnCalls:    make(map[string]struct{}),
		unnarrated:   make(map[string]struct{}),
	}
}

// trackNarration records whether the turn that issued a tool call spoke before it.
// Gemini usually calls a write tool in a silent turn and explains the result only after it arrives,
// so the scheduling of that result depends on this.
func (s *liveSession) trackNarration(events []insights.RealtimeEvent) {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	for _, ev := range events {
		switch ev.Type {
		case insights.RealtimeEventToolCall:
			if ev.CallID == "" {
				continue
			}
			s.pendingCalls[ev.CallID] = struct{}{}
			s.turnCalls[ev.CallID] = struct{}{}
			if !s.turnNarrated {
				s.unnarrated[ev.CallID] = struct{}{}
			}
		case insights.RealtimeEventAudioDelta:
			s.markNarrated()
		case insights.RealtimeEventTranscriptAgent:
			if strings.TrimSpace(ev.Text) != "" {
				s.markNarrated()
			}
		case insights.RealtimeEventTurnComplete, insights.RealtimeEventInterrupted:
			s.turnNarrated = false
			s.turnCalls = make(map[string]struct{})
		}
	}
}

// markNarrated records that the running turn spoke, so its calls are already
// explained and their results must stay silent.
func (s *liveSession) markNarrated() {
	s.turnNarrated = true
	for callID := range s.turnCalls {
		delete(s.unnarrated, callID)
	}
}

// forgetToolCalls drops calls the provider cancelled before a result.
func (s *liveSession) forgetToolCalls(ids []string) {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	for _, id := range ids {
		delete(s.pendingCalls, id)
		delete(s.turnCalls, id)
		delete(s.unnarrated, id)
	}
}

// settleToolCall forgets one answered call and reports whether its turn already
// spoke and whether every sibling call has settled too.
func (s *liveSession) settleToolCall(callID string) (narrated, settled bool) {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	delete(s.pendingCalls, callID)
	delete(s.turnCalls, callID)
	settled = len(s.pendingCalls) == 0

	_, unnarrated := s.unnarrated[callID]
	delete(s.unnarrated, callID)
	return !unnarrated, settled
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

// isReadOnlyTool reports whether a tool only reads state, so the model must wait
// for its payload instead of speaking over it.
func isReadOnlyTool(toolName string) bool {
	_, ok := readOnlyTools[toolName]
	return ok || strings.HasPrefix(toolName, "ext_")
}

// toolResponseScheduling decides whether a tool result resumes generation.
//
// A result that arrives while a sibling call is still running stays silent:
// resuming there starts a second generation beside the one the last result will
// trigger, which is how one tool call turns into two identical ones.
//
// Once settled, a read tool feeds the answer, so the model must speak after it.
// A write tool whose turn already narrated the action only adds its
// acknowledgement to the context; resuming generation there makes Gemini repeat
// the tool or chain more of them. A write tool called in a silent turn is the
// opposite case: that is exactly the turn the model wants the result for in
// order to explain it.
//
// Scheduling is only honoured for NON_BLOCKING declarations (see
// buildLiveFunctionDeclarations); the rest keeps Gemini's WHEN_IDLE default.
func toolResponseScheduling(toolName string, isError, narrated, settled bool) genai.FunctionResponseScheduling {
	if !settled {
		return genai.FunctionResponseSchedulingSilent
	}
	// A failure may need a retry or a spoken recovery.
	if isError || isReadOnlyTool(toolName) || !narrated {
		return genai.FunctionResponseSchedulingWhenIdle
	}
	return genai.FunctionResponseSchedulingSilent
}

func (s *liveSession) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	if toolName == "" {
		return errors.New("google: tool result requires a tool name")
	}

	narrated, settled := s.settleToolCall(callID)
	scheduling := toolResponseScheduling(toolName, isError, narrated, settled)
	s.logger.WithFields(logrus.Fields{
		"callId":     callID,
		"tool":       toolName,
		"isError":    isError,
		"narrated":   narrated,
		"settled":    settled,
		"scheduling": scheduling,
	}).Debug("gemini live: sending tool result")

	err := s.send(func() error {
		return s.conn.SendToolResponse(genai.LiveToolResponseInput{
			FunctionResponses: []*genai.FunctionResponse{{
				ID:         callID,
				Name:       toolName,
				Response:   functionResponsePayload(toolName, resultJSON, isError),
				Scheduling: scheduling,
			}},
		})
	})
	if err != nil {
		return err
	}
	if scheduling == genai.FunctionResponseSchedulingWhenIdle {
		// The model composes a turn from this result, so the status moves on
		// from the tool to the answer that follows it.
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	}
	return nil
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
