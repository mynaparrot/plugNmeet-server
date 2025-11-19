package insights

import (
	"context"
	"io"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// TranscriptionOptions defines the structure for options passed to the transcription service.
type TranscriptionOptions struct {
	SpokenLang string   `json:"spokenLang"`
	TransLangs []string `json:"transLangs"`
}

// TranscriptionResult is the standardized struct for a single piece of transcribed text.
type TranscriptionResult struct {
	Lang         string            `json:"lang"`
	Text         string            `json:"text"`
	IsPartial    bool              `json:"is_partial"`
	Translations map[string]string `json:"translations"` // A map of target language -> translated text
}

// TranslationTaskOptions defines the structure for options passed to the translation service.
type TranslationTaskOptions struct {
	Text        string   `json:"text"`
	SourceLang  string   `json:"source_lang"`
	TargetLangs []string `json:"target_langs"`
}

// TextTranslationResult is the standardized struct for a single text translation result.
type TextTranslationResult struct {
	Text         string            `json:"text"`
	SourceLang   string            `json:"source_lang"`
	Translations map[string]string `json:"translations"` // map of target language -> translated text
}

// TranscriptionStream defines a universal, bidirectional interface for a live transcription.
// It is the contract that all providers must fulfill to offer real-time STT.
// The user of this interface can Write() audio to the stream and will receive
// results by reading from the Results() channel.
type TranscriptionStream interface {
	// Writer accepts a chunk of audio data to be sent to the provider.
	io.Writer

	// Closer signals that the audio stream is finished and no more data will be sent.
	io.Closer

	// SetProperty allows setting provider-specific properties on the fly.
	SetProperty(key string, value string) error

	// Results returns a read-only channel where the transcription results will be sent.
	Results() <-chan *TranscriptionResult
}

// Provider is the master interface for all AI services.
type Provider interface {
	// CreateTranscription initializes a real-time transcription stream for speech-to-text.
	CreateTranscription(ctx context.Context, roomID, userID string, options []byte) (TranscriptionStream, error)

	// TranslateText performs stateless translation of a given text string to one or more languages.
	// It returns a channel that will yield a single result and then close.
	TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (<-chan *TextTranslationResult, error)

	// GetSupportedLanguages is primarily for Transcription & Translation services.
	GetSupportedLanguages(serviceName string) []config.LanguageInfo
}

// Task defines the interface for any runnable, self-contained AI task.
type Task interface {
	// RunAudioStream starts the task's processing pipeline for a continuous audio stream.
	RunAudioStream(ctx context.Context, audioStream <-chan []byte, roomName, identity string, options []byte) error

	// RunStateless executes a single, stateless task (e.g., text translation).
	// It returns a channel that will yield a single result and then close.
	RunStateless(ctx context.Context, options []byte) (interface{}, error)
}
