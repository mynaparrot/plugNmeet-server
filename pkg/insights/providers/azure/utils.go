package azure

import (
	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// azureTranscribeStream is our internal implementation of the insights.TranscriptionStream interface.
// It acts as an adapter, wrapping the Azure-specific PushAudioInputStream and recognizer.
type azureTranscribeStream struct {
	pushStream *audio.PushAudioInputStream
	recognizer *speech.SpeechRecognizer // Use TranslationRecognizer to handle both
	results    chan *insights.TranscriptionResult
}

// Write implements the io.Writer interface by calling the underlying push stream's Write method.
func (s *azureTranscribeStream) Write(p []byte) (n int, err error) {
	err = s.pushStream.Write(p)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close implements the io.Closer interface. It stops the recognizer and closes the stream.
func (s *azureTranscribeStream) Close() error {
	// Stop the recognizer first. This will trigger SessionStopped events.
	err := <-s.recognizer.StopContinuousRecognitionAsync()
	if err != nil {
		return err
	}
	// Now close the underlying audio stream.
	s.pushStream.Close()
	return nil
}

// SetProperty is a placeholder for now, as Azure properties are set at config time.
func (s *azureTranscribeStream) SetProperty(key string, value string) error {
	return s.pushStream.SetPropertyByName(key, value)
}

// Results implements the TranscriptionStream interface.
func (s *azureTranscribeStream) Results() <-chan *insights.TranscriptionResult {
	return s.results
}
