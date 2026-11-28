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

	// liveSilencePeak is the loudest absolute sample a PCM16 chunk may carry and
	// still count as silence. Live streams silent PCM (zeros or a low dither
	// floor) down the whole session timeline; speech peaks far above it.
	liveSilencePeak = 200

	// liveAppendMaxRunes bounds session.commentary.append,
	// session.instructions.append and session.thinking.append. The Live API rejects
	// any append above 500 tokens, and a single rune can already cost one token, so
	// the cap stays far below the limit.
	liveAppendMaxRunes = 400
	// liveTruncationNote replaces the dropped tail of a bounded append and tells
	// the speech model where the full text went.
	liveTruncationNote = "… [truncated; full text is in the conversation]"

	// liveDelegationAckDelayMs holds back the automatic spoken acknowledgement of
	// a delegation. A small delay stops the Live model from promising work
	// ("yep, on it, I'll open it and draw…") that a fast tool
	// (notepad_write, whiteboard_*, create_quiz) finishes first, so the spoken
	// result does not repeat the promise. Higher values are rejected by the API,
	// which caps this field at 800 ms.
	liveDelegationAckDelayMs int64 = 800

	// Live server events.
	liveEventSessionStarted     = "session.started"
	liveEventSessionClosed      = "session.closed"
	liveEventError              = "error"
	liveEventOutputAudioDelta   = "session.output_audio.delta"
	liveEventInputTranscript    = "session.input_transcript.delta"
	liveEventOutputTranscript   = "session.output_transcript.delta"
	liveEventResponse           = "response.event"
	liveEventSessionUsageUpdate = "session.usage.updated"

	// liveEventDelegationCreated reports a turn started on the delegated model.
	liveEventDelegationCreated = "delegation.created"

	// Responses events nested inside response.event.
	liveResponseCreated        = "response.created"
	liveResponseOutputItemDone = "response.output_item.done"
	liveResponseCompleted      = "response.completed"
	liveResponseIncomplete     = "response.incomplete"
	liveResponseCancelled      = "response.cancelled"
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
		delegation := live.ResponsesDelegationConfigParam{
			Model: delegationModel,
			Tools: buildLiveDelegationTools(opts.Tools),
		}
		if opts.SystemInstruction != "" {
			// The delegated model plans the answer and runs the tools, so it needs
			// the same boundary and host instructions as the speech model.
			delegation.Instructions = param.NewOpt(opts.SystemInstruction)
		}
		cfg.Delegation = live.SessionConfigDelegationUnionParam{OfResponses: &live.SessionConfigDelegationResponsesParam{
			Responses: delegation,
			// Hold the automatic spoken acknowledgement back, so a fast tool
			// finishes before the model promises the work it already did.
			AcknowledgementDelayMs: param.NewOpt(liveDelegationAckDelayMs),
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

	// toolMu guards the tool call bookkeeping, which the read loop, the tool
	// goroutines and the grouper timer all touch.
	toolMu sync.Mutex
	// queuedToolCalls holds the function calls of a response that is still
	// streaming, so they are released together once response.completed names
	// them all. Only the read loop touches it.
	queuedToolCalls []insights.RealtimeEvent
	// pendingToolCalls holds the call ids that still miss a function_call_output.
	pendingToolCalls map[string]struct{}
	// deferredEvents holds context that arrived during a tool turn; it goes out
	// right before the response.create that resumes the conversation.
	deferredEvents []live.ClientEventUnionParam
	// delegationActive reports whether a delegated turn still owns the answer.
	// Context that arrives meanwhile is deferred: an append would cancel the
	// pending acknowledgement and could collide with the running response.
	delegationActive bool

	// emitMu guards eventsClosed, so a send can never race with the close.
	emitMu       sync.Mutex
	eventsClosed bool

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newLiveSession(conn liveConn, logger *logrus.Entry) (*liveSession, error) {
	s := &liveSession{
		conn:             conn,
		logger:           logger,
		events:           make(chan insights.RealtimeEvent, liveEventBuffer),
		done:             make(chan struct{}),
		pendingToolCalls: make(map[string]struct{}),
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
	if truncated && !triggerResponse {
		// A whiteboard scene graph or a long message is far beyond what a session
		// append may carry: only the delegated Responses model holds the tools, so it
		// gets the full text as a conversation item and the speech model gets the
		// bounded head of it. The item goes first so the text is in place before an
		// append can trigger speech. A commentary append never gets the item: nothing
		// would consume it, so it would fire on the participant's next spoken turn.
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

	return s.dispatch(events...)
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

	return s.dispatch(live.ClientEventParamOfResponseItemCreate(responses.ResponseInputItemMessageParam{
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

	s.toolMu.Lock()
	delete(s.pendingToolCalls, callID)
	// A response that asked for several tools still waits for the others: a
	// response.create sent now is rejected with function_call_outputs_required.
	settled := len(s.pendingToolCalls) == 0
	pendingRemaining := len(s.pendingToolCalls)
	var deferred []live.ClientEventUnionParam
	if settled {
		// The last output settles the delegation too, so the context held back
		// during the turn can go out with it.
		s.delegationActive = false
		deferred = s.deferredEvents
		s.deferredEvents = nil
	}
	s.toolMu.Unlock()

	s.logger.WithFields(logrus.Fields{
		"callId":           callID,
		"tool":             toolName,
		"isError":          isError,
		"settled":          settled,
		"pendingRemaining": pendingRemaining,
		"deferredFlushed":  len(deferred),
	}).Debug("openai live: sending tool result")

	events := make([]live.ClientEventUnionParam, 0, 2+len(deferred))
	events = append(events, live.ClientEventParamOfResponseItemCreate(item))
	events = append(events, deferred...)
	if settled && needsResponseCreate(deferred) {
		events = append(events, live.ClientEventUnionParam{OfResponseCreate: &live.ResponseCreateEventParam{}})
	}

	return s.send(events...)
}

// needsResponseCreate reports whether no event of the batch already asks the
// model to answer: a commentary append triggers a response on its own, and a
// second trigger collides with the response it started.
func needsResponseCreate(events []live.ClientEventUnionParam) bool {
	for _, ev := range events {
		if ev.OfSessionCommentaryAppend != nil {
			return false
		}
	}
	return true
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

// dispatch sends context now, or holds it back while a delegated turn runs. An
// event that would make the delegated model answer in between is rejected with
// function_call_outputs_required, and an append would cancel the pending
// acknowledgement of the delegation the participant is waiting for.
func (s *liveSession) dispatch(events ...live.ClientEventUnionParam) error {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	if s.hasPendingWorkLocked() {
		s.deferredEvents = append(s.deferredEvents, events...)
		return nil
	}
	return s.send(events...)
}

// hasPendingWork reports whether a delegated turn still owns the answer, so a
// filler promise must not close the turn before the tool output arrives.
func (s *liveSession) hasPendingWork() bool {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()
	return s.hasPendingWorkLocked()
}

// hasPendingWorkLocked is hasPendingWork for callers that hold toolMu.
func (s *liveSession) hasPendingWorkLocked() bool {
	return len(s.pendingToolCalls) > 0 || s.delegationActive
}

// setDelegationActive records that a delegated turn started or settled.
func (s *liveSession) setDelegationActive(active bool) {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	s.delegationActive = active
}

// flushDeferredEvents sends the context held back while the delegation ran.
func (s *liveSession) flushDeferredEvents(deferred []live.ClientEventUnionParam) {
	if len(deferred) == 0 {
		return
	}
	if err := s.send(deferred...); err != nil {
		s.logger.WithError(err).Warn("openai live: could not flush the context deferred during the delegation")
	}
}

// completeResponse registers every tool call of the finished response and
// reports the ones to hand to the caller. The whole set is registered before the
// first call is emitted, so a fast tool can never resume the conversation while a
// sibling call of the same response is still unanswered. The context queued while
// the delegation ran is released only when no tool output is missing anymore.
func (s *liveSession) completeResponse(rawResponse any) ([]insights.RealtimeEvent, []live.ClientEventUnionParam) {
	calls := s.queuedToolCalls
	s.queuedToolCalls = nil

	// response.completed repeats the output items; any call that was not streamed
	// separately is picked up here.
	response, _ := rawResponse.(map[string]any)
	output, _ := response["output"].([]any)
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		seen[call.CallID] = struct{}{}
	}
	for _, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok || nestedString(item, "type") != liveItemTypeFunctionCall {
			continue
		}
		call := newLiveToolCallEvent(item)
		if _, ok = seen[call.CallID]; ok {
			continue
		}
		seen[call.CallID] = struct{}{}
		calls = append(calls, call)
	}

	s.toolMu.Lock()
	defer s.toolMu.Unlock()
	// Merge instead of replacing: an overlapping response must not drop a call
	// that is still waiting for its function_call_output.
	if s.pendingToolCalls == nil {
		s.pendingToolCalls = make(map[string]struct{}, len(calls))
	}
	for _, call := range calls {
		if call.CallID != "" {
			s.pendingToolCalls[call.CallID] = struct{}{}
		}
	}
	if len(s.pendingToolCalls) > 0 {
		// The delegated turn keeps waiting for a function_call_output.
		s.delegationActive = true
		return calls, nil
	}

	s.delegationActive = false
	deferred := s.deferredEvents
	s.deferredEvents = nil
	return calls, deferred
}

// resetToolTurn drops the bookkeeping of a failed response: its calls can no
// longer be answered, so waiting for them would block every later turn.
func (s *liveSession) resetToolTurn() {
	s.toolMu.Lock()
	defer s.toolMu.Unlock()

	s.queuedToolCalls = nil
	s.pendingToolCalls = make(map[string]struct{})
	s.deferredEvents = nil
	s.delegationActive = false
}

// newLiveToolCallEvent builds the tool call event of one function_call item.
func newLiveToolCallEvent(item map[string]any) insights.RealtimeEvent {
	callID := nestedString(item, "call_id")
	if callID == "" {
		callID = nestedString(item, "id")
	}
	return insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   callID,
		ToolName: nestedString(item, "name"),
		ArgsJSON: defaultArgsJSON(nestedString(item, "arguments")),
	}
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
		s.stopSpeakingTimer()
		s.grouper.Close()
		err = s.conn.Close()
	})
	return err
}

// emit publishes one event. The close and the send share emitMu, so a timer or
// callback that fires during shutdown can never write to a closed channel.
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

// closeEvents ends the event stream. It shares emitMu with emit, so it cannot
// run concurrently with a send.
func (s *liveSession) closeEvents() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.eventsClosed {
		return
	}
	s.eventsClosed = true
	close(s.events)
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
		if final && s.endTurn() && !s.hasPendingWork() {
			// A filler promise ("on it…") is not the end of the turn while the
			// delegation still runs: its settled response closes the turn.
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
	if s.isClosed() {
		// A closed session must not leave a timer behind.
		return
	}
	if s.speakingTimer != nil {
		s.speakingTimer.Stop()
	}
	s.speakingTimer = time.AfterFunc(liveSpeakingIdleTimeout, s.endIdleTurn)
}

// stopSpeakingTimer disarms the idle timer, so no callback fires after shutdown.
func (s *liveSession) stopSpeakingTimer() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.speakingTimer != nil {
		s.speakingTimer.Stop()
		s.speakingTimer = nil
	}
}

// endIdleTurn reports the completion of a turn whose audio stream went silent.
func (s *liveSession) endIdleTurn() {
	if !s.endTurn() {
		return
	}
	if s.hasPendingWork() {
		// The spoken promise is followed by silence while the tool runs, so the
		// turn stays open and the settled delegation completes it.
		s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
		return
	}
	s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
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

// hasAudiblePCM16 reports whether a PCM16 little-endian chunk carries speech
// rather than the silence or dither noise Live streams between turns.
func hasAudiblePCM16(pcm []byte) bool {
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		if sample >= liveSilencePeak || sample <= -liveSilencePeak {
			return true
		}
	}
	return false
}

func (s *liveSession) handleServerEvent(msg live.ServerEventUnion) {
	switch msg.Type {
	case liveEventOutputAudioDelta:
		pcm, err := base64.StdEncoding.DecodeString(msg.OfOutputAudioDeltaEvent.Delta)
		if err != nil || len(pcm) == 0 {
			return
		}
		// Silence must not re-arm the speaking turn: it would keep the agent in
		// SPEAKING and reopen a turn that just completed. The publisher pads gaps
		// with silence on its own, so silent chunks are simply dropped.
		if !hasAudiblePCM16(pcm) {
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

	case liveEventDelegationCreated:
		// The delegated turn owns the answer from here on: context waits until
		// it settles instead of cancelling its acknowledgement.
		s.setDelegationActive(true)
		if !s.isSpeaking() {
			// The delegated model owns the answer now, so the voice is quiet
			// until it settles.
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
		}
		s.logger.Debug("openai live: delegation created")

	case liveEventError:
		event := msg.OfErrorEvent
		s.emit(insights.RealtimeEvent{
			Type: insights.RealtimeEventError,
			Err:  fmt.Errorf("openai live: %s (%s)", event.Error.Message, event.Error.Code),
		})

	default:
		// Unknown events still show what the delegated model is doing.
		s.logger.WithField("eventType", msg.Type).Debug("openai live: server event")
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
	case liveResponseCreated:
		// A delegated response is running: an append now would cancel its acknowledgement and
		// a response.create would collide with it.
		s.setDelegationActive(true)
		if !s.isSpeaking() {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
		}
		s.logger.WithField("responseId", nestedString(nestedMap(ev.Event, "response"), "id")).Debug("openai live: delegated response started")

	case liveResponseOutputItemDone:
		item, ok := ev.Event["item"].(map[string]any)
		if !ok || nestedString(item, "type") != liveItemTypeFunctionCall {
			return
		}
		// The response may still emit further calls, so the batch stays queued
		// until response.completed names all of them.
		call := newLiveToolCallEvent(item)
		s.queuedToolCalls = append(s.queuedToolCalls, call)
		s.logger.WithFields(logrus.Fields{
			"callId":  call.CallID,
			"tool":    call.ToolName,
			"argsLen": len(call.ArgsJSON),
		}).Debug("openai live: delegated function call item ready")

	case liveResponseCompleted:
		// The completed response reports what the delegated text model spent.
		s.emitDelegationUsage(ev.Event["response"])

		calls, deferred := s.completeResponse(ev.Event["response"])
		s.logger.WithField("toolCalls", len(calls)).Debug("openai live: delegated response completed")
		if len(calls) > 0 {
			// A function call turn is answered by the caller through SendToolResult.
			for _, call := range calls {
				s.emit(call)
			}
			return
		}
		// Nothing is pending any more, so the held back context goes out now.
		s.flushDeferredEvents(deferred)

		// While the agent is talking, the audio stream reports the turn end.
		if !s.isSpeaking() {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}

	case liveResponseIncomplete, liveResponseCancelled:
		// The delegated turn stopped early: report the usage it still spent and
		// release whatever calls it already completed.
		response := nestedMap(ev.Event, "response")
		reason := nestedString(nestedMap(response, "incomplete_details"), "reason")
		if reason == "" {
			reason = nestedString(response, "status")
		}
		s.emitDelegationUsage(response)

		calls, deferred := s.completeResponse(response)
		if len(calls) > 0 {
			s.logger.WithFields(logrus.Fields{
				"eventType": eventType,
				"reason":    reason,
				"toolCalls": len(calls),
			}).Warn("openai live: delegated response ended early but emitted complete tool calls")
			for _, call := range calls {
				s.emit(call)
			}
			return
		}

		// Nothing can be answered any more, so waiting for an output would block
		// every later turn; the agent is told the turn is over instead.
		s.resetToolTurn()
		s.logger.WithFields(logrus.Fields{
			"eventType": eventType,
			"reason":    reason,
		}).Warn("openai live: delegated response ended without completing")
		s.flushDeferredEvents(deferred)
		if !s.isSpeaking() {
			s.emit(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
		}

	case liveResponseFailed, liveEventError:
		s.resetToolTurn()
		s.logger.WithField("error", liveResponseErrorMessage(ev.Event)).Warn("openai live: delegated response failed")
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
	defer s.closeEvents()
	defer func() {
		if err := s.shutdown(); err != nil {
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
