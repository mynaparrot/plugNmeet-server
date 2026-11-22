package google

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

const (
	defaultTranscribeLiveModel = "gemini-3.5-transcribe-live"
	transcriptionEventBuffer   = 64
)

// transcriptionConn is the subset of *genai.Session used by transcriptionStream.
// Keeping it narrow lets tests drive the stream with a fake socket.
type transcriptionConn interface {
	SendRealtimeInput(input genai.LiveRealtimeInput) error
	Receive() (*genai.LiveServerMessage, error)
	Close() error
}

// liveTranscriptionDialer opens a transcription Live socket.
type liveTranscriptionDialer func(ctx context.Context, model string, config *genai.LiveConnectConfig) (transcriptionConn, error)

// CreateTranscription opens a Gemini Live session that transcribes one participant.
func (p *GoogleProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	model := p.serviceOption("model", defaultTranscribeLiveModel)
	mode := p.serviceOption("transcribe_mode", string(genai.AudioTranscriptionConfigModeVerbatim))

	conn, err := p.transcriptionDialer()(ctx, model, buildTranscriptionConfig(opts, mode))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gemini live transcription: %w", err)
	}

	logger := p.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"model":  model,
	})

	return newTranscriptionStream(ctx, conn, opts, p, logger), nil
}

// transcriptionDialer returns the dialer of transcription sockets; tests replace
// it, so no network is needed.
func (p *GoogleProvider) transcriptionDialer() liveTranscriptionDialer {
	if p.transcribeDial != nil {
		return p.transcribeDial
	}
	return func(ctx context.Context, model string, config *genai.LiveConnectConfig) (transcriptionConn, error) {
		return p.client.Live.Connect(ctx, model, config)
	}
}

// buildTranscriptionConfig maps our transcription options onto the Live setup.
// A text response modality keeps the session from generating audio, and the
// transcription mode decides whether fillers are kept (verbatim) or cleaned up.
func buildTranscriptionConfig(opts *insights.TranscriptionOptions, mode string) *genai.LiveConnectConfig {
	if opts == nil {
		opts = &insights.TranscriptionOptions{}
	}

	return &genai.LiveConnectConfig{
		ResponseModalities: []genai.Modality{genai.ModalityText},
		InputAudioTranscription: &genai.AudioTranscriptionConfig{
			Mode:          transcriptionMode(mode),
			LanguageCodes: transcribeLanguageCodes(opts.SpokenLang),
		},
	}
}

// transcriptionMode accepts our config value; anything unrecognised falls back
// to the word-for-word transcript.
func transcriptionMode(value string) genai.AudioTranscriptionConfigMode {
	if strings.EqualFold(strings.TrimSpace(value), string(genai.AudioTranscriptionConfigModeSmart)) {
		return genai.AudioTranscriptionConfigModeSmart
	}
	return genai.AudioTranscriptionConfigModeVerbatim
}

// transcriptionStream implements insights.TranscriptionStream on a Live socket.
type transcriptionStream struct {
	conn     transcriptionConn
	opts     *insights.TranscriptionOptions
	provider *GoogleProvider
	logger   *logrus.Entry

	ctx    context.Context
	cancel context.CancelFunc

	results chan *insights.TranscriptionEvent
	done    chan struct{}

	// sendMu serialises writes: the websocket accepts one writer at a time.
	sendMu    sync.Mutex
	closeOnce sync.Once
	wg        sync.WaitGroup

	// emitMu guards resultsClosed, so a send can never race with the close.
	emitMu        sync.Mutex
	resultsClosed bool
}

func newTranscriptionStream(ctx context.Context, conn transcriptionConn, opts *insights.TranscriptionOptions, provider *GoogleProvider, logger *logrus.Entry) *transcriptionStream {
	if opts == nil {
		opts = &insights.TranscriptionOptions{}
	}
	streamCtx, cancel := context.WithCancel(ctx)

	s := &transcriptionStream{
		conn:     conn,
		opts:     opts,
		provider: provider,
		logger:   logger,
		ctx:      streamCtx,
		cancel:   cancel,
		results:  make(chan *insights.TranscriptionEvent, transcriptionEventBuffer),
		done:     make(chan struct{}),
	}

	// The socket is already open, so the session counts as ready right away.
	s.emit(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})

	s.wg.Add(1)
	go s.readLoop()

	return s
}

func (s *transcriptionStream) Results() <-chan *insights.TranscriptionEvent {
	return s.results
}

// SetProperty is a no-op: Gemini Live transcription takes no live dials.
func (s *transcriptionStream) SetProperty(key, value string) error {
	return nil
}

func (s *transcriptionStream) WriteSample(sample media.PCM16Sample) error {
	if len(sample) == 0 {
		return nil
	}
	return s.sendPCM(encodePCM16(sample))
}

// sendPCM forwards one chunk as it arrives. Gemini runs its own voice activity
// detection, so withholding silence would keep the server from closing a
// segment and would delay the final transcript.
func (s *transcriptionStream) sendPCM(pcm []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	select {
	case <-s.done:
		return errLiveSessionClosed
	default:
	}

	if err := s.conn.SendRealtimeInput(genai.LiveRealtimeInput{
		Audio: &genai.Blob{Data: pcm, MIMEType: liveAudioMIMEType},
	}); err != nil {
		return fmt.Errorf("google: failed to send live audio: %w", err)
	}
	return nil
}

// Close is idempotent: it cancels in-flight translations and closes the socket.
func (s *transcriptionStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		s.cancel()
		err = s.conn.Close()
	})
	s.wg.Wait()
	return err
}

// encodePCM16 converts our samples to little-endian PCM bytes.
func encodePCM16(sample media.PCM16Sample) []byte {
	out := make([]byte, len(sample)*2)
	for i, value := range sample {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
	}
	return out
}

// readLoop owns the results channel: it closes the channel when it returns.
func (s *transcriptionStream) readLoop() {
	defer s.wg.Done()
	defer s.closeResults()
	defer func() {
		if err := s.conn.Close(); err != nil {
			s.logger.WithError(err).Debug("gemini transcription close returned an error")
		}
	}()

	for {
		msg, err := s.conn.Receive()
		if err != nil {
			// A closed stream is the caller's own doing, not a provider failure.
			if s.ctx.Err() == nil {
				s.logger.WithError(err).Warn("gemini transcription receive failed")
				s.emit(&insights.TranscriptionEvent{Type: insights.EventTypeError, Error: err.Error()})
				s.emitStopped()
			}
			return
		}
		if msg == nil {
			continue
		}
		if msg.GoAway != nil {
			s.logger.Infof("gemini transcription session ends in %s", msg.GoAway.TimeLeft)
			s.emitStopped()
			return
		}
		s.handleServerContent(msg.ServerContent)
	}
}

func (s *transcriptionStream) handleServerContent(content *genai.LiveServerContent) {
	if content == nil {
		return
	}

	if interim := content.InterimInputTranscription; interim != nil {
		if text := strings.TrimSpace(interim.Text); text != "" {
			s.emit(&insights.TranscriptionEvent{
				Type:   insights.EventTypePartialResult,
				Result: s.result(text, true, interim.LanguageCode),
			})
		}
	}

	if final := content.InputTranscription; final != nil {
		if text := strings.TrimSpace(final.Text); text != "" {
			s.dispatchFinal(text, final.LanguageCode)
		}
	}
}

// dispatchFinal emits the final segment. Translations run in the background so
// the receive loop keeps up with the audio.
func (s *transcriptionStream) dispatchFinal(text, lang string) {
	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		result := s.result(text, false, lang)

		if len(s.opts.TransLangs) > 0 && s.provider != nil {
			translation, err := s.provider.runTranslation(s.ctx, text, s.opts.SpokenLang, s.opts.TransLangs)
			switch {
			case err != nil:
				if s.ctx.Err() == nil {
					s.logger.WithError(err).Warn("failed to translate the final transcript")
				}
			case translation != nil:
				result.Translations = translation.Translations
			}
		}

		s.emit(&insights.TranscriptionEvent{Type: insights.EventTypeFinalResult, Result: result})
	}()
}

func (s *transcriptionStream) result(text string, partial bool, detectedLang string) *plugnmeet.InsightsTranscriptionResult {
	// Clients match subtitles against the short locale they selected, so keep
	// the spoken language when known and normalise the detected code otherwise.
	lang := GetLocaleFromCode(s.opts.SpokenLang)
	if lang == "" {
		lang = GetLocaleFromCode(detectedLang)
	}

	return &plugnmeet.InsightsTranscriptionResult{
		FromUserId:                  s.opts.UserName,
		FromUserName:                s.opts.UserName,
		Lang:                        lang,
		Text:                        text,
		IsPartial:                   partial,
		AllowedTranscriptionStorage: s.opts.AllowedTranscriptionStorage,
		Translations:                make(map[string]string),
	}
}

// emit publishes one event. The close and the send share emitMu, so a goroutine
// that fires during shutdown can never write to a closed channel.
func (s *transcriptionStream) emit(event *insights.TranscriptionEvent) bool {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.resultsClosed {
		return false
	}

	select {
	case s.results <- event:
		return true
	case <-s.done:
		return false
	}
}

// emitStopped tells the task to reconnect instead of treating the stream as live.
func (s *transcriptionStream) emitStopped() {
	s.emit(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
}

// closeResults ends the results stream. It shares emitMu with emit, so it cannot
// run concurrently with a send.
func (s *transcriptionStream) closeResults() {
	s.emitMu.Lock()
	defer s.emitMu.Unlock()

	if s.resultsClosed {
		return
	}
	s.resultsClosed = true
	close(s.results)
}
