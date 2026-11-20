package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/openai/openai-go/v3/live"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/sirupsen/logrus"
)

const (
	defaultLiveModel     = string(live.SessionConfigModelGPTLive1)
	liveAudioSampleRate  = 24000
	liveHandshakeTimeout = 15 * time.Second
	liveCloseTimeout     = 2 * time.Second
	liveEventBuffer      = 256

	// liveBargeInRunes is how many spoken letters or digits must arrive while the
	// agent speaks before the transcript counts as a barge-in.
	liveBargeInRunes = 2

	// liveAppendMaxRunes bounds session.commentary.append,
	// session.instructions.append and session.thinking.append. The Live API rejects
	// any append above 500 tokens, and a single rune can already cost one token, so
	// the cap stays far below the limit.
	liveAppendMaxRunes = 400
	// liveTruncationNote replaces the dropped tail of a bounded append and tells
	// the speech model where the full text went.
	liveTruncationNote = "… [truncated; full text is in the conversation]"

	// Live server events.
	liveEventSessionStarted     = "session.started"
	liveEventSessionClosed      = "session.closed"
	liveEventError              = "error"
	liveEventOutputAudioDelta   = "session.output_audio.delta"
	liveEventInputTranscript    = "session.input_transcript.delta"
	liveEventOutputTranscript   = "session.output_transcript.delta"
	liveEventResponse           = "response.event"
	liveEventSessionUsageUpdate = "session.usage.updated"

	// Responses events nested inside response.event.
	liveResponseOutputItemDone = "response.output_item.done"
	liveResponseCompleted      = "response.completed"
	liveResponseFailed         = "response.failed"

	liveSessionClosedExpired  = "expired"
	liveSessionCloseRequested = "close_requested"

	liveSpeakerUser          = "user"
	liveSpeakerAssistant     = "assistant"
	liveItemTypeFunctionCall = "function_call"
	liveItemTypeMessage      = "message"
)

// liveSpeakingIdleTimeout is how long the agent audio may stay silent before the
// speaking turn counts as finished. Chunks arrive every 20-200ms while the model
// speaks, so a longer gap means it stopped — even when neither a transcript nor
// a response event reports the end of the turn. A variable so tests can shorten it.
var liveSpeakingIdleTimeout = 1200 * time.Millisecond

var errLiveSessionClosed = errors.New("openai: live session is closed")

// liveConn is the subset of *live.LiveConnection used here, so the session stays
// testable without a socket.
type liveConn interface {
	Send(ctx context.Context, event live.ClientEventUnionParam) error
	Recv(ctx context.Context) (live.ServerEventUnion, error)
	Close() error
}

// createLiveSession opens a Live session, which delegates tool work to a
// Responses backend instead of exposing tools to the speech model directly.
func (p *OpenAIProvider) createLiveSession(ctx context.Context, model string, opts *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	if model = strings.TrimSpace(model); model == "" {
		model = defaultLiveModel
	}

	// The speech model never sees the tools: every tool turn runs on the
	// delegated Responses backend, which therefore must be configured.
	delegationModel := strings.TrimSpace(p.realtimeOption(opts, "delegation_model", ""))
	if delegationModel == "" {
		return nil, fmt.Errorf("openai live session requires delegation_model to be configured")
	}

	voice := opts.Voice
	if voice == "" {
		voice = p.serviceOption("default_voice", "")
	}

	conn, err := p.openAiClient.Live.Connect(ctx, live.LiveConnectionOptions{}, p.realtimeRequestOptions()...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to openai live session: %w", err)
	}

	return startLiveSession(ctx, conn, buildLiveSessionConfig(model, delegationModel, voice, opts), p.logger, opts.MaxSessionDuration)
}

func buildLiveSessionConfig(model, delegationModel, voice string, opts *insights.RealtimeSessionOptions) live.SessionConfigParam {
	cfg := live.SessionConfigParam{
		Model: live.SessionConfigModel(model),
		Audio: live.SessionConfigAudioParam{
			Format: live.AudioFormatParamOfAudioPCM(liveAudioSampleRate),
		},
	}

	if opts.SystemInstruction != "" {
		cfg.Instructions = param.NewOpt(opts.SystemInstruction)
	}

	if voice != "" {
		cfg.Audio.Output.Voice = live.SessionConfigAudioOutputVoiceUnionParam{OfString: param.NewOpt(voice)}
	}

	if delegationModel != "" || len(opts.Tools) > 0 {
		cfg.Delegation = live.SessionConfigDelegationUnionParam{OfResponses: &live.SessionConfigDelegationResponsesParam{
			Responses: live.ResponsesDelegationConfigParam{
				Model: delegationModel,
				Tools: buildLiveDelegationTools(opts.Tools),
			},
		}}
	}

	return cfg
}

func buildLiveDelegationTools(tools []*insights.MCPToolSpec) []live.ResponsesDelegationConfigToolUnionParam {
	params := make([]live.ResponsesDelegationConfigToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Name == "" {
			continue
		}
		function := live.FunctionToolParam{
			Name:        tool.Name,
			Description: param.NewOpt(tool.Description),
			Parameters:  tool.InputSchema,
		}
		params = append(params, live.ResponsesDelegationConfigToolUnionParam{OfFunction: &function})
	}
	return params
}

// startLiveSession performs the session.start handshake before returning, so a
// rejected configuration surfaces as a connection error.
func startLiveSession(ctx context.Context, conn liveConn, cfg live.SessionConfigParam, logger *logrus.Entry, maxDuration time.Duration) (insights.RealtimeSession, error) {
	if err := conn.Send(ctx, live.ClientEventParamOfSessionStart(cfg)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to start openai live session: %w", err)
	}

	handshakeCtx, cancel := context.WithTimeout(ctx, liveHandshakeTimeout)
	defer cancel()

	var pending []live.ServerEventUnion
	started := false
	for !started {
		msg, err := conn.Recv(handshakeCtx)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("failed to wait for openai live session: %w", err)
		}

		switch msg.Type {
		case liveEventSessionStarted:
			started = true
		case liveEventError:
			event := msg.OfErrorEvent
			_ = conn.Close()
			return nil, fmt.Errorf("openai live: %s (%s)", event.Error.Message, event.Error.Code)
		case liveEventSessionClosed:
			_ = conn.Close()
			return nil, fmt.Errorf("openai live session closed before start: %s", msg.OfSessionClosedEvent.Reason)
		default:
			pending = append(pending, msg)
		}
	}

	s, err := newLiveSession(conn, logger)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	s.pending = pending

	s.wg.Add(1)
	go s.readLoop()
	go s.supervise(ctx, maxDuration)

	return s, nil
}

// liveSession implements insights.RealtimeSession on top of a Live socket.
type liveSession struct {
	conn    liveConn
	logger  *logrus.Entry
	grouper *live.TranscriptGrouper

	events chan insights.RealtimeEvent
	done   chan struct{}

	// pending holds events received before session.started; readLoop replays them
	// first, so it is only touched by the read loop goroutine.
	pending []live.ServerEventUnion

	// stateMu guards the turn state: transcript callbacks may run on the
	// grouper's own timer goroutine.
	stateMu  sync.Mutex
	speaking bool
	// turnEnded records that the current speaking turn already reported its
	// completion, so a turn is closed exactly once.
	turnEnded bool
	// speakingTimer closes the turn when the audio stream goes idle; a provider
	// that never reports the end of a turn would otherwise stay "speaking".
	speakingTimer *time.Timer
	// speechRunes counts spoken characters of the current agent turn; noisy
	// input transcripts must not end the turn on their own.
	speechRunes      int
	lastUsageSeconds float64
	functionCallSeen bool

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newLiveSession(conn liveConn, logger *logrus.Entry) (*liveSession, error) {
	s := &liveSession{
		conn:   conn,
		logger: logger,
		events: make(chan insights.RealtimeEvent, liveEventBuffer),
		done:   make(chan struct{}),
	}

	grouper, err := live.NewTranscriptGrouper(live.TranscriptGrouperOptions{OnUpdate: s.onTranscriptUpdate})
	if err != nil {
		return nil, fmt.Errorf("failed to create openai live transcript grouper: %w", err)
	}
	s.grouper = grouper

	return s, nil
}

func (s *liveSession) Events() <-chan insights.RealtimeEvent {
	return s.events
}

func (s *liveSession) SendAudio(pcm16kMono []byte) error {
	if len(pcm16kMono) == 0 {
		return nil
	}

	pcm := resamplePCM16Bytes16kTo24k(pcm16kMono)
	if len(pcm) == 0 {
		return nil
	}

	return s.send(live.ClientEventParamOfSessionInputAudioAppend(base64.StdEncoding.EncodeToString(pcm)))
}

func (s *liveSession) SendContextText(role, text string, triggerResponse bool) error {
	if text == "" {
		return nil
	}

	appendText, truncated := boundLiveAppend(text, liveAppendMaxRunes)

	events := make([]live.ClientEventUnionParam, 0, 2)
	if truncated {
		// A whiteboard scene graph or a long message is far beyond what a session
		// append may carry: only the delegated Responses model holds the tools, so it
		// gets the full text as a conversation item and the speech model gets the
		// bounded head of it. The item goes first so the text is in place before an
		// append can trigger speech.
		events = append(events, live.ClientEventParamOfResponseItemCreate(responses.ResponseInputItemMessageParam{
			Type:    liveItemTypeMessage,
			Role:    liveSpeakerUser,
			Content: responses.ResponseInputMessageContentListParam{responses.ResponseInputContentParamOfInputText(text)},
		}))
	}

	// delegation_id is required but nullable: null marks general session
	// context, while an empty string is rejected by the API. The convenience
	// helpers always send a value, so the events are built here.
	delegationID := param.Null[string]()

	switch {
	case triggerResponse:
		// Commentary is spoken to the participant, so it must trigger a response.
		events = append(events, live.ClientEventUnionParam{
			OfSessionCommentaryAppend: &live.CommentaryAppendEventParam{Content: appendText, DelegationID: delegationID},
		})
	case role == "system" || role == "developer":
		events = append(events, live.ClientEventUnionParam{
			OfSessionInstructionsAppend: &live.InstructionsAppendEventParam{Content: appendText, DelegationID: delegationID},
		})
	default:
		events = append(events, live.ClientEventUnionParam{
			OfSessionThinkingAppend: &live.ThinkingAppendEventParam{Content: appendText, DelegationID: delegationID},
		})
	}

	return s.send(events...)
}

// boundLiveAppend shortens text so that a session append can never exceed the
// Live API's 500 token limit. It reports whether the text was cut.
func boundLiveAppend(text string, max int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= max {
		return text, false
	}
	keep := max - len([]rune(liveTruncationNote)) - 1
	if keep < 0 {
		keep = 0
	}
	return string(runes[:keep]) + " " + liveTruncationNote, true
}

func (s *liveSession) SendImage(mimeType string, data []byte, caption string) error {
	if len(data) == 0 {
		return nil
	}
	if mimeType == "" {
		mimeType = "image/webp"
	}

	content := make(responses.ResponseInputMessageContentListParam, 0, 2)
	image := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
	image.OfInputImage.ImageURL = param.NewOpt(fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(data)))
	content = append(content, image)

	if caption != "" {
		content = append(content, responses.ResponseInputContentParamOfInputText(caption))
	}

	return s.send(live.ClientEventParamOfResponseItemCreate(responses.ResponseInputItemMessageParam{
		Type:    liveItemTypeMessage,
		Role:    liveSpeakerUser,
		Content: content,
	}))
}

func (s *liveSession) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	if callID == "" {
		return errors.New("openai: live tool result requires a call id")
	}

	item := responses.ResponseInputItemFunctionCallOutputParam{CallID: param.NewOpt(callID)}
	item.Output.OfString = param.NewOpt(formatToolOutputJSON(resultJSON, isError))

	return s.send(
		live.ClientEventParamOfResponseItemCreate(item),
		live.ClientEventUnionParam{OfResponseCreate: &live.ResponseCreateEventParam{}},
	)
}

// Interrupt is a no-op: the Live model reacts to barge-in server side.
func (s *liveSession) Interrupt() error {
	return nil
}

func (s *liveSession) Close() error {
	err := s.shutdown()
	s.wg.Wait()
	return err
}

func (s *liveSession) send(events ...live.ClientEventUnionParam) error {
	if s.isClosed() {
		return errLiveSessionClosed
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	for _, ev := range events {
		if err := s.conn.Send(context.Background(), ev); err != nil {
			return fmt.Errorf("openai: failed to send live event: %w", err)
		}
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

// shutdown asks the server to finalize the session, then closes the socket and
// the grouper exactly once. The read loop owns the events channel.
func (s *liveSession) shutdown() error {
	var err error
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), liveCloseTimeout)
		defer cancel()
		// Best effort: the server may already be gone.
		_ = s.conn.Send(ctx, live.ClientEventUnionParam{OfSessionClose: &live.SessionCloseEventParam{}})

		close(s.done)
		s.grouper.Close()
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

// onTranscriptUpdate is called by the grouper outside its lock.
func (s *liveSession) onTranscriptUpdate(u live.TranscriptUpdate) {
	final := u.Reason != ""

	switch u.Segment.Speaker {
	case liveSpeakerUser:
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: u.Segment.Text, Final: final})
	case liveSpeakerAssistant:
		if !final {
			s.beginTurn()
		}
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: u.Segment.Text, Final: final})
		if final && s.endTurn() {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}
	}
}

// beginTurn reopens the turn bookkeeping for a turn that starts without audio,
// so a transcript-only turn still reports its completion.
func (s *liveSession) beginTurn() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if !s.speaking {
		s.turnEnded = false
	}
}

// setSpeaking updates the agent-speaking flag and reports the previous value.
func (s *liveSession) setSpeaking(speaking bool) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	previous := s.speaking
	if speaking && !previous {
		// Each agent turn counts barge-in characters from scratch.
		s.speechRunes = 0
		s.turnEnded = false
	}
	s.speaking = speaking
	return previous
}

// markSpeaking starts or extends the current agent turn and re-arms the idle
// timer, which closes the turn even when no other event announces its end.
func (s *liveSession) markSpeaking() {
	s.setSpeaking(true)

	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.speakingTimer != nil {
		s.speakingTimer.Stop()
	}
	s.speakingTimer = time.AfterFunc(liveSpeakingIdleTimeout, s.endIdleTurn)
}

// endIdleTurn reports the completion of a turn whose audio stream went silent.
func (s *liveSession) endIdleTurn() {
	if s.endTurn() {
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	}
}

// endTurn closes the current agent turn and reports whether this call owns it,
// so every turn is completed at most once no matter which signal ends it.
func (s *liveSession) endTurn() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	owned := !s.turnEnded
	s.speaking = false
	s.speechRunes = 0
	if s.speakingTimer != nil {
		s.speakingTimer.Stop()
		s.speakingTimer = nil
	}
	if owned {
		s.turnEnded = true
	}
	return owned
}

func (s *liveSession) isSpeaking() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.speaking
}

// noteUserSpeech counts the spoken characters of a user input transcript delta
// and reports whether the agent was interrupted, at most once per agent turn.
// Clicks, taps and room noise transcribe as whitespace, punctuation or a single
// stray character, which must not cut the agent off mid-sentence.
func (s *liveSession) noteUserSpeech(delta string) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if !s.speaking {
		return false
	}

	for _, r := range delta {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			s.speechRunes++
		}
	}

	if s.speechRunes < liveBargeInRunes {
		return false
	}

	s.speaking = false
	s.speechRunes = 0
	return true
}

// setFunctionCallSeen updates the function-call flag and reports the previous
// value, so a completed response knows which turn it closes.
func (s *liveSession) setFunctionCallSeen(seen bool) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	previous := s.functionCallSeen
	s.functionCallSeen = seen
	return previous
}

func (s *liveSession) handleServerEvent(msg live.ServerEventUnion) {
	switch msg.Type {
	case liveEventOutputAudioDelta:
		pcm, err := base64.StdEncoding.DecodeString(msg.OfOutputAudioDeltaEvent.Delta)
		if err != nil || len(pcm) == 0 {
			return
		}
		s.markSpeaking()
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: pcm})

	case liveEventInputTranscript:
		// The participant spoke over the agent: report the barge-in once.
		if s.noteUserSpeech(msg.OfInputTranscriptDeltaEvent.Delta) {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})
		}
		s.pushTranscript(msg, insights.RealtimeEventTranscriptUser, msg.OfInputTranscriptDeltaEvent.Delta)

	case liveEventOutputTranscript:
		s.pushTranscript(msg, insights.RealtimeEventTranscriptAgent, msg.OfOutputTranscriptDeltaEvent.Delta)

	case liveEventResponse:
		s.handleResponseEvent(msg.OfResponseEvent)

	case liveEventSessionUsageUpdate:
		s.handleUsageEvent(msg.OfSessionUsageUpdatedEvent)

	case liveEventSessionClosed:
		// Let the grouper flush whatever it still holds.
		_ = s.grouper.Push(msg)

		switch reason := string(msg.OfSessionClosedEvent.Reason); reason {
		case liveSessionClosedExpired:
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventGoAway, Text: "session expired"})
		case "", liveSessionCloseRequested:
		default:
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventError, Err: fmt.Errorf("openai live session closed: %s", reason)})
		}

	case liveEventError:
		event := msg.OfErrorEvent
		s.emit(insights.RealtimeEvent{
			Type: insights.RealtimeEventError,
			Err:  fmt.Errorf("openai live: %s (%s)", event.Error.Message, event.Error.Code),
		})
	}
}

// pushTranscript feeds the grouper and falls back to the raw delta when the
// grouper cannot use the event (no grouping metadata, backpressure).
func (s *liveSession) pushTranscript(msg live.ServerEventUnion, eventType insights.RealtimeEventType, delta string) {
	if err := s.grouper.Push(msg); err == nil {
		return
	}
	if delta == "" {
		return
	}
	s.emit(insights.RealtimeEvent{Type: eventType, Text: delta})
}

func (s *liveSession) handleResponseEvent(ev live.ResponseEvent) {
	eventType, _ := ev.Event["type"].(string)

	switch eventType {
	case liveResponseOutputItemDone:
		item, ok := ev.Event["item"].(map[string]any)
		if !ok || nestedString(item, "type") != liveItemTypeFunctionCall {
			return
		}
		s.setFunctionCallSeen(true)
		callID := nestedString(item, "call_id")
		if callID == "" {
			callID = nestedString(item, "id")
		}
		s.emit(insights.RealtimeEvent{
			Type:     insights.RealtimeEventToolCall,
			CallID:   callID,
			ToolName: nestedString(item, "name"),
			ArgsJSON: defaultArgsJSON(nestedString(item, "arguments")),
		})

	case liveResponseCompleted:
		// The completed response reports what the delegated text model spent.
		s.emitDelegationUsage(ev.Event["response"])

		// A function call turn is answered by the caller through SendToolResult.
		// While the agent is talking, the audio stream reports the turn end.
		if !s.setFunctionCallSeen(false) && !s.isSpeaking() {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}

	case liveResponseFailed, liveEventError:
		s.emit(insights.RealtimeEvent{
			Type: insights.RealtimeEventError,
			Err:  errors.New(liveResponseErrorMessage(ev.Event)),
		})
	}
}

// emitDelegationUsage reports the tokens the delegated model spent on one
// completed response.
func (s *liveSession) emitDelegationUsage(rawResponse any) {
	response, ok := rawResponse.(map[string]any)
	if !ok {
		return
	}
	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return
	}

	inputTokens, outputTokens := nestedUint32(usage, "input_tokens"), nestedUint32(usage, "output_tokens")
	if inputTokens == 0 && outputTokens == 0 {
		return
	}

	// The Responses API reports the modality, cached and cache-write split in the
	// details objects; both the "tokens" and the "token" spellings are accepted.
	inputDetails := nestedMap(usage, "input_tokens_details", "input_token_details")
	outputDetails := nestedMap(usage, "output_tokens_details", "output_token_details")
	cachedDetails := nestedMap(inputDetails, "cached_tokens_details")
	cacheWriteDetails := nestedMap(inputDetails, "cache_write_tokens_details", "cache_creation_tokens_details")

	s.emit(insights.RealtimeEvent{
		Type:                   insights.RealtimeEventUsage,
		DelegationInputTokens:  inputTokens,
		DelegationOutputTokens: outputTokens,

		DelegationCachedInputTokens:     nestedUint32(inputDetails, "cached_tokens"),
		DelegationCacheWriteInputTokens: firstNonZero(nestedUint32(inputDetails, "cache_write_tokens"), nestedUint32(inputDetails, "cache_creation_input_tokens")),
		DelegationTextInputTokens:       nestedUint32(inputDetails, "text_tokens"),
		DelegationAudioInputTokens:      nestedUint32(inputDetails, "audio_tokens"),
		DelegationImageInputTokens:      nestedUint32(inputDetails, "image_tokens"),

		DelegationCachedTextInputTokens:  nestedUint32(cachedDetails, "text_tokens"),
		DelegationCachedAudioInputTokens: nestedUint32(cachedDetails, "audio_tokens"),
		DelegationCachedImageInputTokens: nestedUint32(cachedDetails, "image_tokens"),

		DelegationCacheWriteTextInputTokens:  nestedUint32(cacheWriteDetails, "text_tokens"),
		DelegationCacheWriteAudioInputTokens: nestedUint32(cacheWriteDetails, "audio_tokens"),
		DelegationCacheWriteImageInputTokens: nestedUint32(cacheWriteDetails, "image_tokens"),

		DelegationTextOutputTokens:     nestedUint32(outputDetails, "text_tokens"),
		DelegationAudioOutputTokens:    nestedUint32(outputDetails, "audio_tokens"),
		DelegationThinkingOutputTokens: nestedUint32(outputDetails, "reasoning_tokens"),
	})
}

// handleUsageEvent reports the audio consumed since the previous update; the
// server sends a cumulative duration.
func (s *liveSession) handleUsageEvent(ev live.SessionUsageUpdatedEvent) {
	s.stateMu.Lock()
	delta := ev.Usage.Seconds - s.lastUsageSeconds
	s.lastUsageSeconds = ev.Usage.Seconds
	s.stateMu.Unlock()

	if delta <= 0 {
		return
	}
	if millis := uint64(math.Round(delta * 1000)); millis > 0 {
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventUsage, AudioMillis: millis})
	}
}

func liveResponseErrorMessage(event map[string]any) string {
	if response, ok := event["response"].(map[string]any); ok {
		if details, ok := response["error"].(map[string]any); ok {
			if message := nestedString(details, "message"); message != "" {
				return message
			}
		}
	}
	if details, ok := event["error"].(map[string]any); ok {
		if message := nestedString(details, "message"); message != "" {
			return message
		}
	}
	if message := nestedString(event, "message"); message != "" {
		return message
	}

	return "response failed"
}

func nestedString(m map[string]any, key string) string {
	value, ok := m[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// nestedMap reads the first object found under one of the keys; a missing key
// yields a nil map, which every nested read treats as empty.
func nestedMap(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := m[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

// firstNonZero returns the first count that is set, so a provider that renamed a
// field still reports it under the old name.
func firstNonZero(values ...uint32) uint32 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

// nestedUint32 reads an unsigned count from a decoded JSON object. Decoders
// differ in the numeric type they produce, so every integer shape is accepted;
// negative values clamp to 0 and oversized ones to math.MaxUint32.
func nestedUint32(m map[string]any, key string) uint32 {
	switch value := m[key].(type) {
	case float64:
		return clampUint32(int64(value))
	case int64:
		return clampUint32(value)
	case int:
		return clampUint32(int64(value))
	case uint32:
		return value
	case uint64:
		if value > math.MaxUint32 {
			return math.MaxUint32
		}
		return uint32(value)
	case json.Number:
		if parsed, err := value.Int64(); err == nil {
			return clampUint32(parsed)
		}
	}
	return 0
}

func clampUint32(value int64) uint32 {
	if value <= 0 {
		return 0
	}
	if value > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(value)
}

func (s *liveSession) readLoop() {
	defer s.wg.Done()
	defer close(s.events)
	defer func() {
		if err := s.shutdown(); err != nil && s.logger != nil {
			s.logger.WithError(err).Debug("openai live session close returned an error")
		}
	}()

	for _, msg := range s.pending {
		s.handleServerEvent(msg)
	}
	s.pending = nil

	for {
		msg, err := s.conn.Recv(context.Background())
		if err != nil {
			if !s.isClosed() {
				s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventError, Err: err})
			}
			return
		}
		s.handleServerEvent(msg)
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
