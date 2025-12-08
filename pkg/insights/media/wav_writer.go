package media

import (
	"encoding/binary"
	"io"
	"os"
	"sync"

	"github.com/livekit/media-sdk"
)

// WAVWriter writes PCM data to a WAV file.
type WAVWriter struct {
	writer      io.WriteSeeker
	onClose     func()
	sampleRate  uint32
	numChannels uint32

	// Add a mutex to protect concurrent access to numBytes
	mu       sync.Mutex
	numBytes uint32
}

// NewWAVWriter creates a new WAVWriter.
func NewWAVWriter(out io.WriteSeeker, sampleRate, numChannels uint32, onClose func()) (*WAVWriter, error) {
	w := &WAVWriter{
		writer:      out,
		onClose:     onClose,
		sampleRate:  sampleRate,
		numChannels: numChannels,
	}

	// Write the initial WAV header with placeholder sizes.
	if err := w.writeHeader(); err != nil {
		return nil, err
	}

	return w, nil
}

// WriteSample writes a PCM sample to the file.
func (w *WAVWriter) WriteSample(sample media.PCM16Sample) error {
	// Lock before writing to protect numBytes
	w.mu.Lock()
	defer w.mu.Unlock()

	err := binary.Write(w.writer, binary.LittleEndian, sample)
	if err != nil {
		return err
	}
	w.numBytes += uint32(len(sample) * 2) // 2 bytes per int16 sample
	return nil
}

// Close finalizes the WAV file by updating the header with the correct sizes.
func (w *WAVWriter) Close() error {
	if err := w.updateHeader(); err != nil {
		return err
	}
	if w.onClose != nil {
		w.onClose()
	}
	// If the underlying writer is a file, close it.
	if f, ok := w.writer.(*os.File); ok {
		return f.Close()
	}
	return nil
}

// SampleRate returns the sample rate of the audio.
func (w *WAVWriter) SampleRate() int {
	return int(w.sampleRate)
}

// String returns a string representation of the writer.
func (w *WAVWriter) String() string {
	return "WAVWriter"
}

// writeHeader writes the initial WAV header with placeholder values for size fields.
func (w *WAVWriter) writeHeader() error {
	// RIFF chunk
	if _, err := w.writer.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint32(0)); err != nil { // Placeholder for file size
		return err
	}
	if _, err := w.writer.Write([]byte("WAVE")); err != nil {
		return err
	}

	// "fmt " sub-chunk
	if _, err := w.writer.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint32(16)); err != nil { // Sub-chunk size (16 for PCM)
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint16(1)); err != nil { // Audio format (1 for PCM)
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint16(w.numChannels)); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, w.sampleRate); err != nil {
		return err
	}
	byteRate := w.sampleRate * w.numChannels * 2 // SampleRate * NumChannels * BitsPerSample/8
	if err := binary.Write(w.writer, binary.LittleEndian, byteRate); err != nil {
		return err
	}
	blockAlign := uint16(w.numChannels * 2) // NumChannels * BitsPerSample/8
	if err := binary.Write(w.writer, binary.LittleEndian, blockAlign); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint16(16)); err != nil { // Bits per sample
		return err
	}

	// "data" sub-chunk
	if _, err := w.writer.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, uint32(0)); err != nil { // Placeholder for data size
		return err
	}

	return nil
}

// updateHeader seeks back to the beginning of the file and writes the final size values.
func (w *WAVWriter) updateHeader() error {
	// Lock before reading numBytes
	w.mu.Lock()
	defer w.mu.Unlock()

	// File size
	if _, err := w.writer.Seek(4, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, w.numBytes+36); err != nil {
		return err
	}

	// Data size
	if _, err := w.writer.Seek(40, io.SeekStart); err != nil {
		return err
	}
	if err := binary.Write(w.writer, binary.LittleEndian, w.numBytes); err != nil {
		return err
	}

	return nil
}
