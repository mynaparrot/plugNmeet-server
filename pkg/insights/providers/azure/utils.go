package azure

import (
	"context"
	"encoding/binary"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// azureTranscribeStream is our internal implementation of the insights.TranscriptionStream interface.
// It acts as an adapter, wrapping the Azure-specific PushAudioInputStream and recognizer.
type azureTranscribeStream struct {
	pushStream *audio.PushAudioInputStream
	cancel     context.CancelFunc
	results    chan *insights.TranscriptionEvent
}

// WriteSample now implements the new interface method.
// It converts the PCM sample to bytes before sending it to Azure.
func (s *azureTranscribeStream) WriteSample(sample media.PCM16Sample) error {
	// Convert the PCM sample back to bytes for the provider's writer interface.
	byteSlice := make([]byte, len(sample)*2)
	for i, val := range sample {
		binary.LittleEndian.PutUint16(byteSlice[i*2:], uint16(val))
	}
	return s.pushStream.Write(byteSlice)
}

// Close implements the io.Closer interface. It stops the recognizer and closes the stream.
func (s *azureTranscribeStream) Close() error {
	// Stop the recognizer first. This will trigger SessionStopped events.
	s.cancel()
	// Now close the underlying audio stream.
	s.pushStream.Close()
	return nil
}

// SetProperty is a placeholder for now, as Azure properties are set at config time.
func (s *azureTranscribeStream) SetProperty(key string, value string) error {
	return s.pushStream.SetPropertyByName(key, value)
}

// Results implements the TranscriptionStream interface.
func (s *azureTranscribeStream) Results() <-chan *insights.TranscriptionEvent {
	return s.results
}

// azureTTSStream wraps the Azure AudioDataStream and the underlying result
// to ensure all resources are properly closed when the consumer is done.
type azureTTSStream struct {
	stream  *speech.AudioDataStream
	outcome *speech.SpeechSynthesisOutcome
}

// Read reads data from the audio stream.
func (s *azureTTSStream) Read(p []byte) (n int, err error) {
	return s.stream.Read(p)
}

// Close closes the audio stream and the underlying synthesis result, releasing all resources.
func (s *azureTTSStream) Close() error {
	s.stream.Close()
	s.outcome.Close()
	return nil
}
