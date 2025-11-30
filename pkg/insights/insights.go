package insights

import (
	"context"
	"fmt"
	"io"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

const (
	SummarizeJobQueue           = "pnm.insights.jobs.summarize"
	PendingSummarizeJobRedisKey = "pnm:insights:pending_summarize_jobs"
)

// EventType defines the type of transcription lifecycle event.
type EventType string
type BatchJobStatus string

const (
	EventTypeSessionStarted EventType = "session_started"
	EventTypeSessionStopped EventType = "session_stopped"
	EventTypeError          EventType = "error"

	EventTypePartialResult EventType = "partial_result"
	EventTypeFinalResult   EventType = "final_result"

	BatchJobStatusCompleted BatchJobStatus = "completed"
	BatchJobStatusFailed    BatchJobStatus = "failed"
	BatchJobStatusRunning   BatchJobStatus = "running"
)

// TranscriptionEvent is the universal message sent over the results channel.
type TranscriptionEvent struct {
	Type   EventType                              `json:"type"`
	Error  string                                 `json:"error,omitempty"`
	Result *plugnmeet.InsightsTranscriptionResult `json:"result,omitempty"`
}

// ServiceType defines the canonical name for an insights service.
type ServiceType string

// AITaskType defines the type of task for AI text chat.
type AITaskType string

const (
	ServiceTypeTranscription      ServiceType = "transcription"
	ServiceTypeTranslation        ServiceType = "translation"
	ServiceTypeSpeechSynthesis    ServiceType = "speech-synthesis"
	ServiceTypeAITextChat         ServiceType = "ai_text_chat"
	ServiceTypeMeetingSummarizing ServiceType = "meeting_summarizing"

	// AITaskTypeChat is for regular chat interactions.
	AITaskTypeChat AITaskType = "chat"
	// AITaskTypeSummarize is for summarization tasks.
	AITaskTypeSummarize AITaskType = "summarize"
)

// ToServiceType translates the Protobuf enum to our internal Go type.
// This keeps the core logic decoupled from the Protobuf definitions.
func ToServiceType(t plugnmeet.InsightsServiceType) (ServiceType, error) {
	switch t {
	case plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_TRANSCRIPTION:
		return ServiceTypeTranscription, nil
	case plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_TRANSLATION:
		return ServiceTypeTranslation, nil
	case plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_SPEECH_SYNTHESIS:
		return ServiceTypeSpeechSynthesis, nil
	case plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_AI_TEXT_CHAT:
		return ServiceTypeAITextChat, nil
	case plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_MEETING_SUMMARIZING:
		return ServiceTypeMeetingSummarizing, nil
	default:
		return "", fmt.Errorf("unknown or unsupported insights service type: %s", t.String())
	}
}

// FromServiceType translates our internal Go type back to the Protobuf enum.
// This is useful for constructing Protobuf messages to be sent from the server.
func FromServiceType(t ServiceType) (plugnmeet.InsightsServiceType, error) {
	switch t {
	case ServiceTypeTranscription:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_TRANSCRIPTION, nil
	case ServiceTypeTranslation:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_TRANSLATION, nil
	case ServiceTypeSpeechSynthesis:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_SPEECH_SYNTHESIS, nil
	case ServiceTypeAITextChat:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_AI_TEXT_CHAT, nil
	case ServiceTypeMeetingSummarizing:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_MEETING_SUMMARIZING, nil
	default:
		return plugnmeet.InsightsServiceType_INSIGHTS_SERVICE_TYPE_UNSPECIFIED, fmt.Errorf("unknown or unsupported insights service type: %s", t)
	}
}

type InsightsTaskPayload struct {
	Task                               string          `json:"task"`
	ServiceType                        ServiceType     `json:"service_type"`
	RoomId                             string          `json:"room_id"`
	RoomTableId                        uint64          `json:"room_table_id"`
	UserId                             string          `json:"user_id"`
	Options                            []byte          `json:"options"`
	RoomE2EEKey                        *string         `json:"room_e2ee_key"`
	TargetUsers                        map[string]bool `json:"target_users,omitempty"`
	CaptureAllParticipantsTracks       bool            `json:"capture_all_participants_tracks,omitempty"`
	AllowedTransLangs                  []string        `json:"allowed_trans_langs,omitempty"`
	EnabledTranscriptionTransSynthesis bool            `json:"enabled_transcription_trans_synthesis"`
	AgentName                          *string         `json:"agent_name,omitempty"`
	HiddenAgent                        bool            `json:"hidden_agent"`
}

// TranscriptionOptions defines the structure for options passed to the transcription service.
type TranscriptionOptions struct {
	SpokenLang string   `json:"spokenLang"`
	TransLangs []string `json:"transLangs"`
}

// TranslationTaskOptions defines the structure for options passed to the translation service.
type TranslationTaskOptions struct {
	Text        string   `json:"text"`
	SourceLang  string   `json:"source_lang"`
	TargetLangs []string `json:"target_langs"`
}

// SynthesisTaskOptions defines the structure for options passed to the speech synthesis service.
type SynthesisTaskOptions struct {
	Text     string `json:"text"`
	Language string `json:"language"`
	Voice    string `json:"voice"`
}

type SummarizeJobPayload struct {
	RoomTableId uint64 `json:"room_table_id"`
	RoomId      string `json:"room_id"`
	FilePath    string `json:"file_path"`
	Options     []byte `json:"options"`
}

type SummarizePendingJobPayload struct {
	RoomTableId      uint64 `json:"room_table_id"`
	JobId            string `json:"job_id"`
	FileName         string `json:"file_name"`
	OriginalFilePath string `json:"original_file_path"`
	CreatedAt        string `json:"created_at"`
}

// BatchJobResponse is a universal struct to hold the result of a batch job status check.
type BatchJobResponse struct {
	Status           BatchJobStatus
	Error            string
	Summary          string
	PromptTokens     uint32
	CompletionTokens uint32
	TotalTokens      uint32
}

// TranscriptionStream defines a universal, bidirectional interface for a live transcription.
// It is the contract that all providers must fulfill to offer real-time STT.
// The user of this interface can Write() audio to the stream and will receive
// results by reading from the Results() channel.
type TranscriptionStream interface {
	// WriteSample accepts a chunk of audio data to be sent to the provider.
	WriteSample(sample media.PCM16Sample) error

	// Closer signals that the audio stream is finished and no more data will be sent.
	io.Closer

	// SetProperty allows setting provider-specific properties on the fly.
	SetProperty(key string, value string) error

	// Results now returns a channel of generic transcription events.
	Results() <-chan *TranscriptionEvent
}

// Provider is the master interface for all AI services.
type Provider interface {
	// CreateTranscription initializes a real-time transcription stream for speech-to-text.
	CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (TranscriptionStream, error)

	// TranslateText performs stateless translation of a given text string to one or more languages.
	// It returns a channel that will yield a single result and then close.
	TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error)

	// SynthesizeText performs stateless text-to-speech synthesis.
	SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error)

	// GetSupportedLanguages is primarily for Transcription & Translation services.
	GetSupportedLanguages(serviceType ServiceType) []*plugnmeet.InsightsSupportedLangInfo

	// AITextChatStream sends a prompt with history and streams back the AI's response.
	AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error)

	// AIChatTextSummarize summarizes a conversation history.
	AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (summaryText string, promptTokens uint32, completionTokens uint32, err error)

	// StartBatchSummarizeAudioFile uploads a local audio file and starts an asynchronous summarization job.
	// It returns a provider-specific job ID for later status checking.
	StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, userPrompt string) (jobId string, fileName string, err error)

	// CheckBatchJobStatus checks the status of a previously started batch job.
	CheckBatchJobStatus(ctx context.Context, jobId string) (*BatchJobResponse, error)

	// DeleteUploadedFile deletes a file that was previously uploaded to the provider's storage.
	DeleteUploadedFile(ctx context.Context, fileName string) error
}

// Task defines the interface for any runnable, self-contained AI task.
type Task interface {
	// RunAudioStream starts the task's processing pipeline for a continuous audio stream.
	RunAudioStream(ctx context.Context, audioStream <-chan media.PCM16Sample, roomTableId uint64, roomId, userId string, options []byte) error

	// RunStateless executes a single, stateless task (e.g., text translation).
	RunStateless(ctx context.Context, options []byte) (interface{}, error)
}
