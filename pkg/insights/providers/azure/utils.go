package azure

import (
	"context"
	"encoding/binary"
	"io"
	"strings"
	"sync"

	"github.com/Microsoft/cognitive-services-speech-sdk-go/audio"
	"github.com/Microsoft/cognitive-services-speech-sdk-go/speech"
	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// azureTranscribeStream owns one session's SDK objects and releases them once.
type azureTranscribeStream struct {
	log *logrus.Entry

	pushStream  *audio.PushAudioInputStream
	audioConfig *audio.AudioConfig
	config      *speech.SpeechTranslationConfig
	recognizer  *speech.TranslationRecognizer

	results chan *insights.TranscriptionEvent

	// sendMu guards results and its close; sends never block.
	sendMu        sync.Mutex
	resultsClosed bool
	terminalSent  bool

	// mu serializes native Write/SetProperty against resource release.
	mu      sync.Mutex
	closed  bool
	started bool

	cancel context.CancelFunc
	// initDone opens after StartContinuousRecognitionAsync resolved.
	initDone     chan struct{}
	stopCh       chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
	teardownOnce sync.Once
}

func newAzureTranscribeStream(ctx context.Context, pushStream *audio.PushAudioInputStream, audioConfig *audio.AudioConfig, config *speech.SpeechTranslationConfig, recognizer *speech.TranslationRecognizer, log *logrus.Entry) *azureTranscribeStream {
	watchCtx, cancel := context.WithCancel(ctx)
	s := &azureTranscribeStream{
		log:         log,
		pushStream:  pushStream,
		audioConfig: audioConfig,
		config:      config,
		recognizer:  recognizer,
		results:     make(chan *insights.TranscriptionEvent, 64),
		cancel:      cancel,
		initDone:    make(chan struct{}),
		stopCh:      make(chan struct{}, 1),
		done:        make(chan struct{}),
	}

	go func() {
		select {
		case <-watchCtx.Done():
		case <-s.stopCh:
		}
		s.teardown()
	}()

	return s
}

func (s *azureTranscribeStream) Results() <-chan *insights.TranscriptionEvent {
	return s.results
}

func (s *azureTranscribeStream) WriteSample(sample media.PCM16Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.pushStream == nil {
		return io.EOF
	}
	return s.pushStream.Write(pcm16ToBytes(sample))
}

// SetProperty is a placeholder for now, as Azure properties are set at config time.
func (s *azureTranscribeStream) SetProperty(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.pushStream == nil {
		return io.EOF
	}
	return s.pushStream.SetPropertyByName(key, value)
}

// Close is idempotent and waits for the resources and results channel.
func (s *azureTranscribeStream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.requestStop()
	})

	<-s.done
	return nil
}

// startFinished records whether recognition started and opens the init gate.
func (s *azureTranscribeStream) startFinished(started bool) {
	s.mu.Lock()
	s.started = started
	s.mu.Unlock()
	close(s.initDone)
}

func (s *azureTranscribeStream) requestStop() {
	select {
	case s.stopCh <- struct{}{}:
	default:
	}
}

func (s *azureTranscribeStream) send(event *insights.TranscriptionEvent) {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.sendLocked(event)
}

// sendTerminal publishes the only SessionStopped event; a full buffer evicts a
// queued event so the stop signal is never dropped. It never blocks callbacks.
func (s *azureTranscribeStream) sendTerminal() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.terminalSent || s.resultsClosed {
		return
	}
	s.terminalSent = true

	event := &insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped}
	select {
	case s.results <- event:
		return
	default:
	}

	// Full buffer: evict the oldest queued event, which is most likely a partial.
	select {
	case <-s.results:
	default:
	}
	select {
	case s.results <- event:
	default:
		s.log.Warnln("could not deliver terminal transcription event")
	}
}

func (s *azureTranscribeStream) sendLocked(event *insights.TranscriptionEvent) {
	if s.resultsClosed || event == nil {
		return
	}

	select {
	case s.results <- event:
	default:
		s.log.Warnf("resultsChan full, dropping event type=%s", event.Type)
	}
}

func (s *azureTranscribeStream) closeResults() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.resultsClosed {
		return
	}
	s.resultsClosed = true
	close(s.results)
}

func (s *azureTranscribeStream) teardown() {
	s.teardownOnce.Do(func() {
		// Until initDone the SDK objects belong to CreateTranscription.
		<-s.initDone
		s.releaseResources()
	})
}

func (s *azureTranscribeStream) releaseResources() {
	s.mu.Lock()
	s.closed = true
	started := s.started
	s.mu.Unlock()

	// The SDK waits for pending callbacks here, so mu must not be held.
	if started && s.recognizer != nil {
		<-s.recognizer.StopContinuousRecognitionAsync()
	}

	s.mu.Lock()
	if s.recognizer != nil {
		s.recognizer.Close()
	}
	if s.audioConfig != nil {
		s.audioConfig.Close()
	}
	if s.config != nil {
		s.config.Close()
	}
	if s.pushStream != nil {
		s.pushStream.Close()
	}
	s.mu.Unlock()

	s.closeResults()
	close(s.done)
}

func pcm16ToBytes(sample media.PCM16Sample) []byte {
	byteSlice := make([]byte, len(sample)*2)
	for i, val := range sample {
		binary.LittleEndian.PutUint16(byteSlice[i*2:], uint16(val))
	}
	return byteSlice
}

func hasText(text string) bool {
	return strings.TrimSpace(text) != ""
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
