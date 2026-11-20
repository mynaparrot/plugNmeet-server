package insights

import (
	"errors"
	"strings"
	"time"
)

// RealtimeEventType classifies the events a RealtimeSession emits.
type RealtimeEventType string

const (
	// RealtimeEventAudioDelta carries agent speech as 24 kHz PCM16 mono (AudioPCM24k).
	RealtimeEventAudioDelta RealtimeEventType = "audio_delta"
	// RealtimeEventTranscriptUser is the transcription of participant speech.
	RealtimeEventTranscriptUser RealtimeEventType = "transcript_user"
	// RealtimeEventTranscriptAgent is the transcription of agent speech.
	RealtimeEventTranscriptAgent RealtimeEventType = "transcript_agent"
	// RealtimeEventToolCall asks the caller to execute a tool and answer via SendToolResult.
	RealtimeEventToolCall RealtimeEventType = "tool_call"
	// RealtimeEventTurnComplete marks the end of an agent turn.
	RealtimeEventTurnComplete RealtimeEventType = "turn_complete"
	// RealtimeEventInterrupted signals that the agent must stop speaking (barge-in).
	RealtimeEventInterrupted RealtimeEventType = "interrupted"
	// RealtimeEventError carries a terminal session error.
	RealtimeEventError RealtimeEventType = "error"
	// RealtimeEventGoAway signals that the provider will close the socket soon.
	RealtimeEventGoAway RealtimeEventType = "go_away"
	// RealtimeEventUsage reports token usage for cost accounting.
	RealtimeEventUsage RealtimeEventType = "usage"
)

// ErrRealtimeNotSupported is returned by providers without realtime support.
var ErrRealtimeNotSupported = errors.New("insights: realtime session not supported by provider")

// RealtimeEvent is a single item on a session's Events() channel.
// Only the fields relevant to Type are populated.
type RealtimeEvent struct {
	Type RealtimeEventType
	// AudioPCM24k is 24 kHz, 16-bit, mono little-endian PCM for RealtimeEventAudioDelta.
	AudioPCM24k []byte
	// Text is the transcript (transcript events) or a human readable notice.
	Text string
	// Final is true when a transcript is not a partial result.
	Final bool
	// CallID, ToolName and ArgsJSON describe a RealtimeEventToolCall.
	CallID   string
	ToolName string
	ArgsJSON string
	// Err is set for RealtimeEventError.
	Err error
	// InputTokens and OutputTokens are set for RealtimeEventUsage.
	InputTokens  uint32
	OutputTokens uint32
	// CachedInputTokens is the cached prefix of InputTokens, billed at a
	// discounted rate when one is configured for the modality.
	CachedInputTokens uint32
	// CacheWriteInputTokens is the input that was written to the prompt cache,
	// billed at the cache write rate when one is configured.
	CacheWriteInputTokens      uint32
	CacheWriteTextInputTokens  uint32
	CacheWriteAudioInputTokens uint32
	CacheWriteImageInputTokens uint32
	// Per-modality detail of the counts above, so each rate applies to the
	// tokens it belongs to. ImageInputTokens covers image and video input, and
	// TextOutputTokens includes the thinking tokens, which are billed as output.
	TextInputTokens        uint32
	AudioInputTokens       uint32
	ImageInputTokens       uint32
	CachedTextInputTokens  uint32
	CachedAudioInputTokens uint32
	CachedImageInputTokens uint32
	TextOutputTokens       uint32
	AudioOutputTokens      uint32
	ThinkingOutputTokens   uint32
	// DelegationInputTokens and DelegationOutputTokens report the tokens spent by
	// a delegated text model (e.g. an OpenAI Live Responses delegation), which is
	// priced apart from the session model. The details below split them the same
	// way the session tokens are split above.
	DelegationInputTokens  uint32
	DelegationOutputTokens uint32

	DelegationCachedInputTokens          uint32
	DelegationCacheWriteInputTokens      uint32
	DelegationTextInputTokens            uint32
	DelegationAudioInputTokens           uint32
	DelegationImageInputTokens           uint32
	DelegationCachedTextInputTokens      uint32
	DelegationCachedAudioInputTokens     uint32
	DelegationCachedImageInputTokens     uint32
	DelegationCacheWriteTextInputTokens  uint32
	DelegationCacheWriteAudioInputTokens uint32
	DelegationCacheWriteImageInputTokens uint32
	DelegationTextOutputTokens           uint32
	DelegationAudioOutputTokens          uint32
	DelegationThinkingOutputTokens       uint32
	// AudioMillis is the agent audio duration covered by a usage event, when known.
	AudioMillis uint64
	// TraceID correlates the event with server-side logs.
	TraceID string
}

// MCPToolSpec is the provider-agnostic declaration of a tool (MCP shaped).
type MCPToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// CachedExternalMCPTool is one pre-warmed external MCP tool description. It is
// stored in Redis and is the only representation that reaches the registry, so
// a slow remote server never delays a session.
type CachedExternalMCPTool struct {
	ServerKey      string       `json:"server_key"`
	ServerSlug     string       `json:"server_slug"`
	RemoteToolName string       `json:"remote_tool_name"`
	IsResourceRead bool         `json:"is_resource_read,omitempty"`
	Spec           *MCPToolSpec `json:"spec"`
}

// AIQuizDraftQuestion is one question of a drafted quiz.
type AIQuizDraftQuestion struct {
	Question           string   `json:"question"`
	Options            []string `json:"options"`
	CorrectOptionIndex int      `json:"correct_option_index"`
	Explanation        string   `json:"explanation,omitempty"`
}

// AIQuizDraft is a quiz preview waiting for the room host's approval. It is
// stored between the draft_quiz and publish_quiz tool calls, so nothing reaches
// the participants' screens before the preview has been approved in chat.
// Questions holds every drafted question; the top-level Question/Options fields
// remain for single-question compatibility with already stored drafts.
type AIQuizDraft struct {
	DraftID            string                `json:"draft_id"`
	Question           string                `json:"question"`
	Options            []string              `json:"options"`
	CorrectOptionIndex int                   `json:"correct_option_index"`
	Explanation        string                `json:"explanation,omitempty"`
	Questions          []AIQuizDraftQuestion `json:"questions,omitempty"`
	CreatedAt          int64                 `json:"created_at"`
}

// AllQuestions returns the drafted questions, falling back to the legacy
// single-question fields when the draft predates the Questions field.
func (d *AIQuizDraft) AllQuestions() []AIQuizDraftQuestion {
	if d == nil {
		return nil
	}
	if len(d.Questions) > 0 {
		return d.Questions
	}
	if strings.TrimSpace(d.Question) == "" {
		return nil
	}
	return []AIQuizDraftQuestion{{
		Question:           d.Question,
		Options:            d.Options,
		CorrectOptionIndex: d.CorrectOptionIndex,
		Explanation:        d.Explanation,
	}}
}

// RealtimeSessionOptions is the provider-agnostic session configuration.
// SystemInstruction must be built by the caller (it is never filled in here) so
// this package stays free of any dependency on the services layer.
type RealtimeSessionOptions struct {
	SystemInstruction  string
	Voice              string
	SpokenLang         string
	TargetLang         string
	InstructionLang    string
	SpeakingRate       float32
	Tools              []*MCPToolSpec
	MaxSessionDuration time.Duration
	// Extra carries provider specific options.
	Extra map[string]any
}

// RealtimeSession is the bidirectional contract for a live audio session.
// One goroutine reads Events() and one writes audio; implementations must be
// safe for concurrent use.
type RealtimeSession interface {
	// SendAudio sends 16 kHz PCM16 mono little-endian audio.
	SendAudio(pcm16kMono []byte) error

	// SendImage sends an image (e.g. image/webp) plus an optional caption.
	SendImage(mimeType string, data []byte, caption string) error

	// SendContextText injects text that must not look like participant speech.
	// triggerResponse asks the provider to answer immediately. Providers may bound
	// how much of a very long text reaches the speech model, but must keep the whole
	// text available to the model that runs the tools.
	SendContextText(role string, text string, triggerResponse bool) error

	// SendToolResult answers a RealtimeEventToolCall.
	SendToolResult(callID, toolName, resultJSON string, isError bool) error

	// Interrupt asks the provider to stop generating the current turn.
	Interrupt() error

	// Events returns the event stream; it is closed when the session ends.
	Events() <-chan RealtimeEvent

	// Close terminates the session and releases all resources.
	Close() error
}
