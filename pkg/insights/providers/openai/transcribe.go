package openai

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/media-sdk"
	openaisdk "github.com/openai/openai-go/v3"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// chunkedStream implements insights.TranscriptionStream by buffering PCM16
// audio in memory and POSTing fixed-duration WAV chunks to the OpenAI
// transcription endpoint. Each upload yields a single final_result event.
//
// This trades partials and per-word latency for portability: the same code
// path works against OpenAI cloud and any OpenAI-compatible self-hosted
// transcription server (e.g. faster-whisper behind LocalAI, whisper.cpp's
// HTTP server, vLLM with whisper). Real-time partials would require the
// OpenAI Realtime websocket API, which is not yet covered by openai-go and
// is not supported by most self-hosted backends.
type chunkedStream struct {
	client       openaisdk.Client
	model        string
	chunkSamples int
	language     string
	userId       string
	userName     string
	allowStorage bool

	bufMu sync.Mutex
	buf   []int16

	chunks  chan []int16
	results chan *insights.TranscriptionEvent

	closed atomic.Bool
	wg     sync.WaitGroup
	cancel context.CancelFunc

	log *logrus.Entry
}

func newChunkedStream(parentCtx context.Context, client openaisdk.Client, model string, chunkSeconds float64, roomId, userId string, opts *insights.TranscriptionOptions, log *logrus.Entry) (*chunkedStream, error) {
	if chunkSeconds <= 0 {
		chunkSeconds = defaultChunkSeconds
	}
	ctx, cancel := context.WithCancel(parentCtx)

	s := &chunkedStream{
		client:       client,
		model:        model,
		chunkSamples: int(chunkSeconds * float64(transcriptionSampleRate)),
		language:     opts.SpokenLang,
		userId:       userId,
		userName:     opts.UserName,
		allowStorage: opts.AllowedTranscriptionStorage,
		chunks:       make(chan []int16, 4),
		results:      make(chan *insights.TranscriptionEvent, 16),
		cancel:       cancel,
		log: log.WithFields(logrus.Fields{
			"roomId": roomId,
			"userId": userId,
			"lang":   opts.SpokenLang,
			"model":  model,
		}),
	}

	s.wg.Add(1)
	go s.uploadLoop(ctx)

	s.results <- &insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted}
	return s, nil
}

// WriteSample appends PCM16 samples to the in-memory buffer and pushes a chunk
// onto the upload queue once the configured duration has accumulated.
func (s *chunkedStream) WriteSample(sample media.PCM16Sample) error {
	if s.closed.Load() {
		return fmt.Errorf("stream is closed")
	}

	s.bufMu.Lock()
	s.buf = append(s.buf, sample...)
	var ready []int16
	if len(s.buf) >= s.chunkSamples {
		ready = make([]int16, s.chunkSamples)
		copy(ready, s.buf[:s.chunkSamples])
		s.buf = s.buf[s.chunkSamples:]
	}
	s.bufMu.Unlock()

	if ready != nil {
		select {
		case s.chunks <- ready:
		default:
			// Upload queue is saturated. Dropping is preferable to blocking
			// the audio path; the next chunk has fresh content anyway.
			s.log.Warn("openai transcription upload queue full, dropping chunk")
		}
	}
	return nil
}

// Close flushes any buffered tail audio, signals the upload loop to drain,
// and waits for in-flight uploads to finish before returning.
func (s *chunkedStream) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	s.bufMu.Lock()
	tail := s.buf
	s.buf = nil
	s.bufMu.Unlock()

	if len(tail) > 0 {
		select {
		case s.chunks <- tail:
		default:
			s.log.Warn("openai transcription upload queue full at close, dropping tail")
		}
	}
	close(s.chunks)
	s.wg.Wait()
	s.cancel()
	close(s.results)
	return nil
}

// SetProperty is unused; OpenAI's transcription model is selected at session
// creation and does not accept mid-stream property changes.
func (s *chunkedStream) SetProperty(_, _ string) error { return nil }

// Results exposes the read side of the event channel.
func (s *chunkedStream) Results() <-chan *insights.TranscriptionEvent { return s.results }

func (s *chunkedStream) uploadLoop(ctx context.Context) {
	defer s.wg.Done()
	defer func() {
		// Best-effort: always emit a stopped event so downstream consumers
		// can release any room-level state tied to this stream.
		s.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
	}()

	for samples := range s.chunks {
		if len(samples) == 0 {
			continue
		}
		text, err := s.transcribeChunk(ctx, samples)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.WithError(err).Warn("openai transcription upload failed")
			s.safeSend(&insights.TranscriptionEvent{
				Type:  insights.EventTypeError,
				Error: err.Error(),
			})
			continue
		}
		if text == "" {
			continue
		}
		s.safeSend(&insights.TranscriptionEvent{
			Type: insights.EventTypeFinalResult,
			Result: &plugnmeet.InsightsTranscriptionResult{
				FromUserId:                  s.userId,
				FromUserName:                s.userName,
				Lang:                        s.language,
				Text:                        text,
				IsPartial:                   false,
				AllowedTranscriptionStorage: s.allowStorage,
				Translations:                map[string]string{},
			},
		})
	}
}

func (s *chunkedStream) safeSend(ev *insights.TranscriptionEvent) {
	defer func() {
		// A late event after Close() races with channel close; swallow the
		// resulting panic rather than tearing down the upload goroutine.
		_ = recover()
	}()
	select {
	case s.results <- ev:
	default:
		s.log.Warn("openai transcription results channel full, dropping event")
	}
}

func (s *chunkedStream) transcribeChunk(ctx context.Context, samples []int16) (string, error) {
	wav := encodeWAV(samples, transcriptionSampleRate)

	params := openaisdk.AudioTranscriptionNewParams{
		Model: openaisdk.AudioModel(s.model),
		File:  namedReader{Reader: bytes.NewReader(wav), name: "audio.wav"},
	}
	if s.language != "" {
		params.Language = openaisdk.String(s.language)
	}

	// Each chunk gets a generous-but-bounded deadline so a hung backend
	// doesn't pile up requests indefinitely.
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := s.client.Audio.Transcriptions.New(reqCtx, params)
	if err != nil {
		return "", fmt.Errorf("audio.transcriptions.new: %w", err)
	}
	return resp.Text, nil
}

// namedReader exposes a filename to the openai-go multipart encoder, which
// uses it to derive the upload's MIME type. Without a recognised audio
// extension Whisper-compatible servers refuse the upload.
type namedReader struct {
	io.Reader
	name string
}

func (n namedReader) Name() string { return n.name }

// encodeWAV serialises 16-bit signed little-endian PCM samples as a minimal
// canonical RIFF/WAVE file (44-byte header + interleaved data). 16 kHz mono
// matches the audio LiveKit hands us via PCM16Sample.
func encodeWAV(samples []int16, sampleRate int) []byte {
	const (
		numChannels   = 1
		bitsPerSample = 16
	)
	dataSize := len(samples) * 2
	totalSize := 36 + dataSize

	buf := bytes.NewBuffer(make([]byte, 0, 44+dataSize))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(totalSize))
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16)) // PCM fmt chunk size
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // PCM format
	_ = binary.Write(buf, binary.LittleEndian, uint16(numChannels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate*numChannels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(numChannels*bitsPerSample/8))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))

	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	_ = binary.Write(buf, binary.LittleEndian, samples)

	return buf.Bytes()
}
