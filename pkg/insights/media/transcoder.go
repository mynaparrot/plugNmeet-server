package media

import (
	"context"

	"github.com/livekit/media-sdk"
	lkmedia "github.com/livekit/server-sdk-go/v2/pkg/media"
	"github.com/pion/webrtc/v4"
)

const (
	// The sample rate required by most STT providers.
	targetSampleRate = 16000
)

// Transcoder now uses the simplified pcmWriter.
type Transcoder struct {
	writer   *pcmWriter
	pcmTrack *lkmedia.PCMRemoteTrack
}

func NewTranscoder(ctx context.Context, track *webrtc.TrackRemote, decryptor lkmedia.Decryptor) (*Transcoder, error) {
	writer := newPCMWriter()

	opts := []lkmedia.PCMRemoteTrackOption{
		lkmedia.WithTargetSampleRate(targetSampleRate),
	}
	if decryptor != nil {
		opts = append(opts, lkmedia.WithDecryptor(decryptor))
	}

	pcmTrack, err := lkmedia.NewPCMRemoteTrack(track, writer, opts...)
	if err != nil {
		// If creation fails, we must close the writer we just created.
		_ = writer.Close()
		return nil, err
	}

	t := &Transcoder{
		writer:   writer,
		pcmTrack: pcmTrack,
	}

	// Start a goroutine to automatically close all resources when the context is done.
	go func() {
		<-ctx.Done()
		// Close the track first to stop the flow of samples.
		pcmTrack.Close()
		// Then, close our writer to signal the end of the stream to consumers.
		_ = writer.Close()
	}()

	return t, nil
}

// AudioStream returns the read-only channel of transcoded audio.
func (t *Transcoder) AudioStream() <-chan media.PCM16Sample {
	return t.writer.audioChan
}
