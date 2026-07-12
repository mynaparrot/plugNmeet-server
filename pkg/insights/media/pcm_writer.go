package media

import (
	"io"
	"sync"

	"github.com/livekit/media-sdk"
)

// pcmWriter implements the media.Writer interface. It receives resampled
// PCM audio from a PCMRemoteTrack and writes it directly to a Go channel.
type pcmWriter struct {
	audioChan chan media.PCM16Sample
	closeOnce sync.Once
	mu        sync.Mutex
	isClosed  bool
}

// newPCMWriter creates a new writer.
func newPCMWriter() *pcmWriter {
	return &pcmWriter{
		audioChan: make(chan media.PCM16Sample),
	}
}

// WriteSample is called by the PCMRemoteTrack. The sample is already
// resampled to our target sample rate.
func (w *pcmWriter) WriteSample(sample media.PCM16Sample) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.isClosed {
		return io.EOF
	}

	// Use a non-blocking write to avoid stalling the media pipeline.
	select {
	case w.audioChan <- sample:
		return nil
	default:
		// Channel is full, drop the sample.
		return nil
	}
}

// Close closes the writer and the underlying channel safely using sync.Once.
func (w *pcmWriter) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		defer w.mu.Unlock()

		w.isClosed = true
		close(w.audioChan)
	})
	return nil
}
