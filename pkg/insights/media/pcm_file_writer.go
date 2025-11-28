package media

import (
	"encoding/binary"
	"os"

	"github.com/livekit/media-sdk"
)

// PCMFileWriter implements the media.Writer interface to write PCM samples to a file.
type PCMFileWriter struct {
	file    *os.File
	onClose func()
}

type PCMFileWriterOption func(*PCMFileWriter)

func WithOnCloseCallback(f func()) PCMFileWriterOption {
	return func(w *PCMFileWriter) {
		w.onClose = f
	}
}

// NewPCMFileWriter creates a new PCMFileWriter.
func NewPCMFileWriter(filename string, opts ...PCMFileWriterOption) (*PCMFileWriter, error) {
	file, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	w := &PCMFileWriter{file: file}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// WriteSample writes a PCM sample to the file.
func (w *PCMFileWriter) WriteSample(sample media.PCM16Sample) error {
	return binary.Write(w.file, binary.LittleEndian, sample)
}

// Close closes the underlying file and triggers the onClose callback.
func (w *PCMFileWriter) Close() error {
	err := w.file.Close()
	if w.onClose != nil {
		w.onClose()
	}
	return err
}

// SampleRate returns a dummy sample rate. This should be configured if needed.
func (w *PCMFileWriter) SampleRate() int {
	return 48000
}

// String returns a string representation of the writer.
func (w *PCMFileWriter) String() string {
	return "PCMFileWriter"
}
