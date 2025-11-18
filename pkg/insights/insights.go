package insights

import (
	"context"
	"io"
)

// TranscriptionResult is the standardized struct for a single piece of transcribed text.
type TranscriptionResult struct {
	Text      string `json:"text"`
	IsPartial bool   `json:"is_partial"` // True if this is an intermediate, non-final result.
}

// TranscriptionStream defines a universal, bidirectional interface for a live transcription.
// It is the contract that all providers must fulfill to offer real-time STT.
// The user of this interface can Write() audio to the stream and will receive
// results by reading from the Results() channel.
type TranscriptionStream interface {
	// Write accepts a chunk of audio data to be sent to the provider.
	io.Writer

	// Closer signals that the audio stream is finished and no more data will be sent.
	io.Closer

	// SetProperty allows setting provider-specific properties on the fly.
	SetProperty(key string, value string) error

	// Results returns a read-only channel where the transcription results will be sent.
	Results() <-chan *TranscriptionResult
}

// Provider is the master interface for all AI services.
// It defines a contract for any provider we want to support.
type Provider interface {
	// CreateTranscription handles real-time, streaming speech-to-text and,
	CreateTranscription(ctx context.Context, roomId, userId, spokenLang string, options []byte) (TranscriptionStream, error)

	// Translate translates a block of text.
	Translate(ctx context.Context, text string, targetLangs []string) (map[string]string, error)
}

// Task defines the interface for any runnable, self-contained AI task.
type Task interface {
	// Run starts the task's processing pipeline.
	Run(ctx context.Context, audioStream <-chan []byte, roomID, identity string, options []byte) error
}
