package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/livekit/media-sdk"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

const (
	// The sample rate required by most STT providers.
	targetSampleRate = 16000
)

// pcmWriter implements the media.Writer interface. It receives resampled
// PCM audio from a PCMRemoteTrack and writes it directly to a Go channel.
type pcmWriter struct {
	audioChan chan []byte
	isClosed  bool
	lock      sync.Mutex
}

// newPCMWriter creates a new writer.
func newPCMWriter() *pcmWriter {
	return &pcmWriter{
		audioChan: make(chan []byte),
	}
}

// WriteSample is called by the PCMRemoteTrack. The sample is already
// resampled to our target sample rate.
func (w *pcmWriter) WriteSample(sample media.PCM16Sample) error {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.isClosed {
		return io.EOF
	}

	// Create a byte slice of the correct size.
	byteSlice := make([]byte, sample.Size())

	// Use the efficient CopyTo method.
	_, err := sample.CopyTo(byteSlice)
	if err != nil {
		return err
	}

	// Send the final byte slice to our output channel.
	w.audioChan <- byteSlice
	return nil
}

// Close closes the writer and the underlying channel.
func (w *pcmWriter) Close() error {
	w.lock.Lock()
	defer w.lock.Unlock()

	if !w.isClosed {
		w.isClosed = true
		close(w.audioChan)
	}
	return nil
}

// Transcoder now uses the simplified pcmWriter.
type Transcoder struct {
	writer   *pcmWriter
	pcmTrack *lkmedia.PCMRemoteTrack
}

// NewTranscoder now accepts a context for lifecycle management.
func NewTranscoder(ctx context.Context, track *webrtc.TrackRemote) (*Transcoder, error) {
	writer := newPCMWriter()

	pcmTrack, err := lkmedia.NewPCMRemoteTrack(track, writer, lkmedia.WithTargetSampleRate(targetSampleRate))
	if err != nil {
		return nil, err
	}

	t := &Transcoder{
		writer:   writer,
		pcmTrack: pcmTrack,
	}

	// Start a goroutine to automatically close the transcoder when the context is done.
	go func() {
		<-ctx.Done()
		pcmTrack.Close()
	}()

	return t, nil
}

// AudioStream returns the read-only channel of transcoded audio.
func (t *Transcoder) AudioStream() <-chan []byte {
	return t.writer.audioChan
}

// --- Publisher for outgoing audio ---

// AudioPublisher handles publishing a local PCM audio track to a LiveKit room.
type AudioPublisher struct {
	track *lkmedia.PCMLocalTrack
}

// NewAudioPublisher creates and publishes a new local audio track to the room.
func NewAudioPublisher(room *lksdk.Room, trackName string, sampleRate int, numChannels int) (*AudioPublisher, error) {
	// 1. Create a new local PCM track.
	localTrack, err := lkmedia.NewPCMLocalTrack(sampleRate, numChannels, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create local PCM track: %w", err)
	}

	// 2. Publish the track to the room.
	if _, err = room.LocalParticipant.PublishTrack(localTrack, &lksdk.TrackPublicationOptions{
		Name: trackName,
	}); err != nil {
		return nil, fmt.Errorf("failed to publish track: %w", err)
	}

	return &AudioPublisher{
		track: localTrack,
	}, nil
}

// WriteSample writes a PCM sample to the published track.
// This is how the service will send synthesized audio back to the room.
func (p *AudioPublisher) WriteSample(sample media.PCM16Sample) error {
	if p.track == nil {
		return errors.New("publisher is closed or not initialized")
	}
	return p.track.WriteSample(sample)
}

// Close unpublishes and closes the local track.
func (p *AudioPublisher) Close() {
	if p.track != nil {
		p.track.Close()
	}
}
