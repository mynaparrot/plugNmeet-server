package media

import (
	"errors"
	"fmt"
	"sync"

	"github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
)

type AudioPublisher struct {
	track     *lkmedia.PCMLocalTrack
	closeOnce sync.Once
}

// NewAudioPublisher creates and publishes a new local audio track to the room.
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

	return &AudioPublisher{
		track: localTrack,
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

	return len(data), nil
}

// WriteSample writes a PCM sample to the published track.
// This is how the service will send synthesized audio back to the room.
func (p *AudioPublisher) WriteSample(sample media.PCM16Sample) error {
	if p.track == nil {
		return errors.New("publisher is not initialized")
	}
	return p.track.WriteSample(sample)
}

// Close unpublishes and closes the local track safely using sync.Once.
func (p *AudioPublisher) Close() {
	p.closeOnce.Do(func() {
		if p.track != nil {
			_ = p.track.Close()
		}
	})
}
