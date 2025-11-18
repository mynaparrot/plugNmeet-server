package insights

import (
	"context"
	"io"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// TranscriptionResult is the standardized struct for a single piece of transcribed text.
type TranscriptionResult struct {
	Lang         string            `json:"lang"`
	Text         string            `json:"text"`
	IsPartial    bool              `json:"is_partial"`
	Translations map[string]string `json:"translations"` // A map of target language -> translated text
}

// TranscriptionOptions defines the structure for options passed to the transcription service.
type TranscriptionOptions struct {
	SpokenLang string   `json:"spokenLang"`
	TransLangs []string `json:"transLangs"`
}

// TranscriptionStream defines a universal, bidirectional interface for a live transcription.
// It is the contract that all providers must fulfill to offer real-time STT.
// The user of this interface can Write() audio to the stream and will receive
// results by reading from the Results() channel.
type TranscriptionStream interface {
	// Write accepts a chunk of audio data to be sent to the provider.
	io.Writer

	// Close signals that the audio stream is finished and no more data will be sent.
	io.Closer

	// SetProperty allows setting provider-specific properties on the fly.
	SetProperty(key string, value string) error

	// Results returns a read-only channel where the transcription results will be sent.
	Results() <-chan *TranscriptionResult
}

// Provider is the master interface for all AI services.
// It defines the contract for any provider we want to support.
type Provider interface {
	// CreateTranscription initializes a real-time transcription stream.
	CreateTranscription(ctx context.Context, roomID, userID string, options []byte) (TranscriptionStream, error)

	// CreateTranscriptionWithTranslation translates a block of text.
	CreateTranscriptionWithTranslation(ctx context.Context, roomID, userID string, options []byte) (TranscriptionStream, error)

	// GetSupportedLanguages mostly for Transcription & Translation
	GetSupportedLanguages(serviceName string) []config.LanguageInfo
}

// Task defines the interface for any runnable, self-contained AI task.
type Task interface {
	// Run starts the task's processing pipeline.
	Run(ctx context.Context, audioStream <-chan []byte, roomName, identity string, options []byte) error
}
