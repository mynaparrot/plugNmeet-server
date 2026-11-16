// Package fake provides a deterministic in-memory insights.Provider and
// insights.RealtimeSession for unit tests. It never dials a network.
package fake

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// ErrNotImplemented is returned by the methods this fake does not model.
var ErrNotImplemented = errors.New("fake: not implemented")

const (
	eventBuffer = 64

	// chatStreamID is the id the fake chat streams report.
	chatStreamID = "fake-stream"
)

// Image is a recorded SendImage call.
type Image struct {
	MIMEType string
	Data     []byte
	Caption  string
}

// ContextText is a recorded SendContextText call.
type ContextText struct {
	Role            string
	Text            string
	TriggerResponse bool
}

// ToolResult is a recorded SendToolResult call.
type ToolResult struct {
	CallID     string
	ToolName   string
	ResultJSON string
	IsError    bool
}

// Session is a scripted insights.RealtimeSession.
type Session struct {
	events chan insights.RealtimeEvent

	mu          sync.Mutex
	audio       [][]byte
	images      []Image
	contexts    []ContextText
	toolResults []ToolResult
	interrupts  int
	closed      bool
}

var _ insights.RealtimeSession = (*Session)(nil)

// NewSession returns an empty session ready to record calls.
func NewSession() *Session {
	return &Session{events: make(chan insights.RealtimeEvent, eventBuffer)}
}

func (s *Session) SendAudio(pcm16kMono []byte) error {
	audio := make([]byte, len(pcm16kMono))
	copy(audio, pcm16kMono)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.audio = append(s.audio, audio)
	return nil
}

func (s *Session) SendImage(mimeType string, data []byte, caption string) error {
	image := Image{MIMEType: mimeType, Data: append([]byte(nil), data...), Caption: caption}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.images = append(s.images, image)
	return nil
}

func (s *Session) SendContextText(role string, text string, triggerResponse bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts = append(s.contexts, ContextText{Role: role, Text: text, TriggerResponse: triggerResponse})
	return nil
}

func (s *Session) SendToolResult(callID, toolName, resultJSON string, isError bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolResults = append(s.toolResults, ToolResult{
		CallID:     callID,
		ToolName:   toolName,
		ResultJSON: resultJSON,
		IsError:    isError,
	})
	return nil
}

func (s *Session) Interrupt() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupts++
	return nil
}

func (s *Session) Events() <-chan insights.RealtimeEvent {
	return s.events
}

// Close closes the event channel once; later Push calls are ignored.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	return nil
}

// Push publishes events to the Events() channel, as a provider would.
func (s *Session) Push(events ...insights.RealtimeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for _, ev := range events {
		s.events <- ev
	}
}

// AudioChunks returns a copy of all audio written via SendAudio.
func (s *Session) AudioChunks() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	chunks := make([][]byte, len(s.audio))
	copy(chunks, s.audio)
	return chunks
}

// Images returns a copy of all images sent.
func (s *Session) Images() []Image {
	s.mu.Lock()
	defer s.mu.Unlock()
	images := make([]Image, len(s.images))
	copy(images, s.images)
	return images
}

// Contexts returns a copy of all injected context texts.
func (s *Session) Contexts() []ContextText {
	s.mu.Lock()
	defer s.mu.Unlock()
	contexts := make([]ContextText, len(s.contexts))
	copy(contexts, s.contexts)
	return contexts
}

// ToolResults returns a copy of all tool results sent.
func (s *Session) ToolResults() []ToolResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]ToolResult, len(s.toolResults))
	copy(results, s.toolResults)
	return results
}

// Interrupts returns how often Interrupt was called.
func (s *Session) Interrupts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupts
}

// ChatRequest is one recorded AITextChatStream / AITextChatStreamWithTools call.
type ChatRequest struct {
	Model   string
	History []*plugnmeet.InsightsAITextChatContent
	// Tools is nil for a plain AITextChatStream call.
	Tools []*insights.MCPToolSpec
}

// ScriptedToolCall makes the fake model ask for one tool call.
type ScriptedToolCall struct {
	Name     string
	ArgsJSON string
}

// ToolExecution is one recorded tools.Execute call.
type ToolExecution struct {
	CallID   string
	Name     string
	ArgsJSON string
	Output   string
	IsError  bool
}

// Provider is an insights.Provider that hands out scripted realtime sessions.
type Provider struct {
	// RealtimeErr, when set, is returned by CreateRealtimeSession.
	RealtimeErr error

	// ChatErr, when set, is returned by the AI text chat methods.
	ChatErr error
	// ChatChunks is the text the chat methods stream back, in order.
	ChatChunks []string
	// ChatToolCalls, when set, is requested by AITextChatStreamWithTools before
	// any text is streamed.
	ChatToolCalls []ScriptedToolCall

	mu             sync.Mutex
	sessions       []*Session
	chatRequests   []ChatRequest
	toolExecutions []ToolExecution
}

var _ insights.Provider = (*Provider)(nil)

// NewProvider returns an empty fake provider.
func NewProvider() *Provider {
	return &Provider{}
}

// CreateRealtimeSession records and returns a fresh session.
func (p *Provider) CreateRealtimeSession(ctx context.Context, model string, opts *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	if p.RealtimeErr != nil {
		return nil, p.RealtimeErr
	}

	session := NewSession()
	p.mu.Lock()
	p.sessions = append(p.sessions, session)
	p.mu.Unlock()

	return session, nil
}

// Sessions returns all sessions created so far.
func (p *Provider) Sessions() []*Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	sessions := make([]*Session, len(p.sessions))
	copy(sessions, p.sessions)
	return sessions
}

func (p *Provider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	return nil
}

// AITextChatStream records the request and streams the scripted chunks.
func (p *Provider) AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	if p.ChatErr != nil {
		return nil, p.ChatErr
	}
	p.mu.Lock()
	p.chatRequests = append(p.chatRequests, ChatRequest{Model: chatModel, History: history})
	chunks := append([]string(nil), p.ChatChunks...)
	p.mu.Unlock()

	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult, eventBuffer)
	go func() {
		defer close(resultChan)
		pushChatChunks(ctx, resultChan, chunks)
		pushLastChatChunk(ctx, resultChan)
	}()

	return resultChan, nil
}

// AITextChatStreamWithTools records the request, runs the scripted tool calls
// through tools.Execute and then streams the scripted chunks, like a provider
// that answers in a second turn.
func (p *Provider) AITextChatStreamWithTools(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent, tools *insights.AITextChatToolRunner) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	if p.ChatErr != nil {
		return nil, p.ChatErr
	}

	request := ChatRequest{Model: chatModel, History: history}
	if tools != nil {
		request.Tools = tools.Tools
	}

	p.mu.Lock()
	p.chatRequests = append(p.chatRequests, request)
	chunks := append([]string(nil), p.ChatChunks...)
	calls := append([]ScriptedToolCall(nil), p.ChatToolCalls...)
	p.mu.Unlock()

	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult, eventBuffer)
	go func() {
		defer close(resultChan)

		if tools != nil && tools.Execute != nil {
			for _, call := range calls {
				callID := call.Name + "-call"
				out, isErr := tools.Execute(ctx, callID, call.Name, call.ArgsJSON)
				p.mu.Lock()
				p.toolExecutions = append(p.toolExecutions, ToolExecution{
					CallID:   callID,
					Name:     call.Name,
					ArgsJSON: call.ArgsJSON,
					Output:   out,
					IsError:  isErr,
				})
				p.mu.Unlock()
			}
		}
		pushChatChunks(ctx, resultChan, chunks)
		pushLastChatChunk(ctx, resultChan)
	}()

	return resultChan, nil
}

// ChatRequests returns a copy of every recorded chat request.
func (p *Provider) ChatRequests() []ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	requests := make([]ChatRequest, len(p.chatRequests))
	copy(requests, p.chatRequests)
	return requests
}

// ToolExecutions returns a copy of every recorded tools.Execute call.
func (p *Provider) ToolExecutions() []ToolExecution {
	p.mu.Lock()
	defer p.mu.Unlock()
	executions := make([]ToolExecution, len(p.toolExecutions))
	copy(executions, p.toolExecutions)
	return executions
}

// pushChatChunks streams the scripted text chunks.
func pushChatChunks(ctx context.Context, resultChan chan<- *plugnmeet.InsightsAITextChatStreamResult, chunks []string) {
	for _, text := range chunks {
		select {
		case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{Id: chatStreamID, Text: text}:
		case <-ctx.Done():
			return
		}
	}
}

// pushLastChatChunk closes the stream the way a real provider does.
func pushLastChatChunk(ctx context.Context, resultChan chan<- *plugnmeet.InsightsAITextChatStreamResult) {
	select {
	case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{Id: chatStreamID, IsLastChunk: true}:
	case <-ctx.Done():
	}
}

func (p *Provider) AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	return "", 0, 0, ErrNotImplemented
}

func (p *Provider) StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, userPrompt string) (string, string, error) {
	return "", "", ErrNotImplemented
}

func (p *Provider) CheckBatchJobStatus(ctx context.Context, jobId string) (*insights.BatchJobResponse, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) DeleteUploadedFile(ctx context.Context, fileName string) error {
	return nil
}
