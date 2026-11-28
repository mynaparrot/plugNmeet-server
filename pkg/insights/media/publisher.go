package media

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
)

const (
	// DefaultSampleRate16k is the PCM sample rate of synthesized (TTS) audio.
	DefaultSampleRate16k = 16000
	// DefaultOutputSampleRate24k is the native agent voice rate of realtime providers.
	DefaultOutputSampleRate24k = 24000
)

type AudioPublisher struct {
	track       *lkmedia.PCMLocalTrack
	sampleRate  int
	numChannels int
	closeOnce   sync.Once

	mu sync.Mutex
	// playoutUntil is when the audio handed to the track finishes playing.
	playoutUntil time.Time
}

// NewAudioPublisher creates and publishes a new local audio track to the room.
// sampleRate is the rate of the PCM written to the track: DefaultSampleRate16k
// for TTS synthesis, DefaultOutputSampleRate24k for realtime agent audio.
func NewAudioPublisher(room *lksdk.Room, trackName string, sampleRate int, numChannels int, e2eeKey *string) (publisher *AudioPublisher, err error) {
	defer func() {
		if err != nil {
			room.Disconnect()
			if publisher != nil {
				publisher.Close()
			}
		}
	}()

	var trackOpts []lkmedia.PCMLocalTrackOption
	pubOpts := &lksdk.TrackPublicationOptions{
		Name:   trackName,
		Source: livekit.TrackSource_MICROPHONE,
	}
	if e2eeKey != nil && *e2eeKey != "" {
		key, err := lksdk.DeriveKeyFromString(*e2eeKey)
		if err != nil {
			return nil, fmt.Errorf("failed to derive key for tts encryptor: %w", err)
		}
		// Use 0 for the key ID as per the GCM standard
		encryptor, err := lkmedia.NewGCMEncryptor(key, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to create tts encryptor: %w", err)
		}
		trackOpts = append(trackOpts, lkmedia.WithEncryptor(encryptor))
		// Set the encryption type on the publication options
		pubOpts.Encryption = livekit.Encryption_GCM
	}

	// 1. Create a new local PCM track.
	localTrack, err := lkmedia.NewPCMLocalTrack(sampleRate, numChannels, nil, trackOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create local PCM track: %w", err)
	}

	// 2. Publish the track to the room.
	if _, err = room.LocalParticipant.PublishTrack(localTrack, pubOpts); err != nil {
		return nil, fmt.Errorf("failed to publish track: %w", err)
	}

	if numChannels <= 0 {
		numChannels = 1
	}
	return &AudioPublisher{
		track:       localTrack,
		sampleRate:  sampleRate,
		numChannels: numChannels,
	}, nil
}

// Write converts the raw PCM byte slice and writes it to the track.
func (p *AudioPublisher) Write(data []byte) (n int, err error) {
	// The data is 16kHz 16-bit mono PCM.
	// We need to convert it to []int16 for the PCMLocalTrack.
	numSamples := len(data) / 2
	if numSamples == 0 {
		return len(data), nil
	}

	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		// Assuming little-endian byte order for 16-bit PCM
		samples[i] = int16(data[i*2]) | int16(data[i*2+1])<<8
	}

	mediaSample := media.PCM16Sample(samples)
	err = p.track.WriteSample(mediaSample)
	if err != nil {
		return 0, err
	}
	p.advancePlayout(len(data) / (2 * p.numChannels))

	return len(data), nil
}

// WriteSample writes a PCM sample to the published track.
// This is how the service will send synthesized audio back to the room.
func (p *AudioPublisher) WriteSample(sample media.PCM16Sample) error {
	if p.track == nil {
		return errors.New("publisher is not initialized")
	}
	if err := p.track.WriteSample(sample); err != nil {
		return err
	}
	p.advancePlayout(len(sample) / p.numChannels)
	return nil
}

// advancePlayout pushes the playout horizon forward by the duration of the
// queued frames, so the caller knows when the room has heard everything written.
func (p *AudioPublisher) advancePlayout(numFrames int) {
	if numFrames <= 0 || p.sampleRate <= 0 {
		return
	}
	duration := time.Duration(numFrames) * time.Second / time.Duration(p.sampleRate)

	p.mu.Lock()
	defer p.mu.Unlock()
	if now := time.Now(); p.playoutUntil.Before(now) {
		p.playoutUntil = now
	}
	p.playoutUntil = p.playoutUntil.Add(duration)
}

// RemainingDuration reports how much of the written audio has not played yet.
func (p *AudioPublisher) RemainingDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playoutUntil.After(time.Now()) {
		return time.Until(p.playoutUntil)
	}
	return 0
}

// EndSegment is a no-op for a track publisher: a live audio track is a single
// continuous stream, so there is no segment to close. It exists so AudioPublisher
// and DataStreamAudioPublisher share one interface.
func (p *AudioPublisher) EndSegment() {}

// Flush drops queued PCM that has not reached the room yet. Used for barge-in so
// the agent stops talking the instant a participant speaks. ClearQueue takes the
// track's own lock, so Flush is safe alongside WriteSample and Close.
func (p *AudioPublisher) Flush() {
	if p.track != nil {
		p.track.ClearQueue()
	}
	p.mu.Lock()
	p.playoutUntil = time.Time{}
	p.mu.Unlock()
}

// Close unpublishes and closes the local track safely using sync.Once.
func (p *AudioPublisher) Close() {
	p.closeOnce.Do(func() {
		if p.track != nil {
			_ = p.track.Close()
		}
	})
}
