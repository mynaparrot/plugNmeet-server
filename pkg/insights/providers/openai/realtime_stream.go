package openai

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/openai/openai-go/v3/shared/constant"
	"github.com/sirupsen/logrus"
)

var _ insights.TranscriptionStream = (*openaiRealtimeStream)(nil)

type openaiTranscriptionSession struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

type openaiRealtimeEvent struct {
	Type       string `json:"type"`
	Delta      string `json:"delta"`
	Transcript string `json:"transcript"`
	Error      struct {
		Message string `json:"message"`
	} `json:"error"`
}

type openaiRealtimeStream struct {
	ctx    context.Context
	cancel context.CancelFunc

	results     chan *insights.TranscriptionEvent
	log         *logrus.Entry
	opts        *insights.TranscriptionOptions
	llmProvider *OpenAIProvider

	closeOnce sync.Once
	closed    atomic.Bool

	writeWg  sync.WaitGroup
	readWg   sync.WaitGroup
	commitWg sync.WaitGroup
	eventWg  sync.WaitGroup

	inputSampleRate int

	transcriptionSession *openaiTranscriptionSession

	wsWriteChan chan []byte

	// Protects wsWriteChan sends and close.
	// This prevents send-on-closed-channel panics.
	writeQueueMu sync.Mutex

	// Guards transcription state.
	mu sync.Mutex

	manualCommit bool

	transcriptionPendingPCM []int16

	transcriptionSegmentSamples   int
	transcriptionSegmentStartedAt time.Time
	transcriptionLastSpeechAt     time.Time
	transcriptionHasSpeech        bool

	transcriptionMinCommitSamples          int
	transcriptionSilenceCommitDuration     time.Duration
	transcriptionMaxCommitDuration         time.Duration
	transcriptionMaxBufferedSilenceSamples int
	transcriptionSpeechRMSThreshold        float64

	partialTranscriptBuilder strings.Builder
}

func newOpenAIRealtimeStream(ctx context.Context, cancel context.CancelFunc, log *logrus.Entry, opts *insights.TranscriptionOptions, llmProvider *OpenAIProvider) *openaiRealtimeStream {
	return &openaiRealtimeStream{
		ctx:         ctx,
		cancel:      cancel,
		results:     make(chan *insights.TranscriptionEvent, 50),
		wsWriteChan: make(chan []byte, 500),

		log:         log,
		opts:        opts,
		llmProvider: llmProvider,

		manualCommit: true,

		transcriptionMinCommitSamples: samplesForDuration(
			defaultRealtimeSampleRate,
			defaultTranscriptionMinCommitMs,
		),
		transcriptionSilenceCommitDuration: time.Duration(defaultTranscriptionSilenceCommitMs) * time.Millisecond,
		transcriptionMaxCommitDuration:     time.Duration(defaultTranscriptionMaxCommitMs) * time.Millisecond,
		transcriptionMaxBufferedSilenceSamples: samplesForDuration(
			defaultRealtimeSampleRate,
			defaultTranscriptionMaxBufferedSilenceMs,
		),
		transcriptionSpeechRMSThreshold: float64(defaultTranscriptionSpeechRMS),
	}
}

func (s *openaiRealtimeStream) WriteSample(sample media.PCM16Sample) error {
	if s.closed.Load() {
		return nil
	}

	if s.transcriptionSession == nil || s.transcriptionSession.conn == nil {
		return fmt.Errorf("openai transcription session is not initialized")
	}

	pcm := pcm16SampleToInt16(sample)
	if len(pcm) == 0 {
		return nil
	}

	if s.inputSampleRate != defaultRealtimeSampleRate {
		pcm = resamplePCM16Linear(pcm, s.inputSampleRate, defaultRealtimeSampleRate)
	}

	if len(pcm) == 0 {
		return nil
	}

	if !s.manualCommit {
		if err := s.queueAudioAppend(pcm); err != nil {
			s.log.WithError(err).Warn("failed to queue openai realtime audio chunk")
		}
		return nil
	}

	now := time.Now()
	hasSpeech := hasSpeechPCM16(pcm, s.transcriptionSpeechRMSThreshold)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return nil
	}

	if hasSpeech {
		if !s.transcriptionHasSpeech {
			s.transcriptionHasSpeech = true
			s.transcriptionSegmentStartedAt = now
			s.transcriptionSegmentSamples = 0

			if len(s.transcriptionPendingPCM) > 0 {
				if err := s.queueAudioAppend(s.transcriptionPendingPCM); err != nil {
					s.log.WithError(err).Warn("failed to queue openai realtime pre-roll audio")
				} else {
					s.transcriptionSegmentSamples += len(s.transcriptionPendingPCM)
				}

				s.transcriptionPendingPCM = s.transcriptionPendingPCM[:0]
			}
		}

		s.transcriptionLastSpeechAt = now
	}

	if s.transcriptionHasSpeech {
		if err := s.queueAudioAppend(pcm); err != nil {
			s.log.WithError(err).Warn("failed to queue openai realtime speech audio")
			return nil
		}

		s.transcriptionSegmentSamples += len(pcm)
		return nil
	}

	s.transcriptionPendingPCM = append(s.transcriptionPendingPCM, pcm...)

	maxPreRollSamples := s.transcriptionMaxBufferedSilenceSamples
	if maxPreRollSamples <= 0 {
		maxPreRollSamples = samplesForDuration(defaultRealtimeSampleRate, defaultTranscriptionMaxBufferedSilenceMs)
	}

	if len(s.transcriptionPendingPCM) > maxPreRollSamples {
		drop := len(s.transcriptionPendingPCM) - maxPreRollSamples
		copy(s.transcriptionPendingPCM, s.transcriptionPendingPCM[drop:])
		s.transcriptionPendingPCM = s.transcriptionPendingPCM[:maxPreRollSamples]
	}

	return nil
}

func (s *openaiRealtimeStream) queueAudioAppend(pcm []int16) error {
	if len(pcm) == 0 {
		return nil
	}

	audioBytes := pcm16ToBytes(pcm)

	payload, err := json.Marshal(map[string]any{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(audioBytes),
	})
	if err != nil {
		return err
	}

	return s.queueWebSocketMessage(payload)
}

func (s *openaiRealtimeStream) queueAudioCommit() error {
	return s.queueWebSocketMessage([]byte(`{"type":"input_audio_buffer.commit"}`))
}

func (s *openaiRealtimeStream) queueAudioCommitDuringClose() error {
	return s.queueWebSocketMessageDuringClose([]byte(`{"type":"input_audio_buffer.commit"}`))
}

func (s *openaiRealtimeStream) queueWebSocketMessage(payload []byte) error {
	s.writeQueueMu.Lock()
	defer s.writeQueueMu.Unlock()

	if s.closed.Load() {
		return nil
	}

	if s.wsWriteChan == nil {
		return nil
	}

	select {
	case s.wsWriteChan <- payload:
		return nil

	case <-s.ctx.Done():
		return s.ctx.Err()

	default:
		return fmt.Errorf("openai realtime websocket outbound queue full")
	}
}

func (s *openaiRealtimeStream) queueWebSocketMessageDuringClose(payload []byte) error {
	s.writeQueueMu.Lock()
	defer s.writeQueueMu.Unlock()

	if s.wsWriteChan == nil {
		return nil
	}

	select {
	case s.wsWriteChan <- payload:
		return nil

	default:
		return fmt.Errorf("openai realtime websocket outbound queue full during close")
	}
}

func (s *openaiRealtimeStream) closeWriteQueue() {
	s.writeQueueMu.Lock()
	defer s.writeQueueMu.Unlock()

	if s.wsWriteChan != nil {
		close(s.wsWriteChan)
		s.wsWriteChan = nil
	}
}

func (s *openaiRealtimeStream) writeLoop() {
	defer s.writeWg.Done()

	for msg := range s.wsWriteChan {
		if s.transcriptionSession == nil || s.transcriptionSession.conn == nil {
			return
		}

		s.transcriptionSession.writeMu.Lock()
		err := s.transcriptionSession.conn.WriteMessage(websocket.TextMessage, msg)
		s.transcriptionSession.writeMu.Unlock()

		if err != nil {
			if s.ctx.Err() == nil && !s.closed.Load() {
				s.log.WithError(err).Error("failed to write openai realtime websocket message")
			}
			return
		}
	}
}

func (s *openaiRealtimeStream) Close() error {
	var closeErr error

	s.closeOnce.Do(func() {
		s.closed.Store(true)

		// Stop commit loop. It also checks closed on each tick.
		s.cancel()
		s.commitWg.Wait()

		if s.manualCommit {
			s.mu.Lock()

			if s.transcriptionHasSpeech &&
				s.transcriptionSegmentSamples >= s.transcriptionMinCommitSamples {
				if err := s.queueAudioCommitDuringClose(); err != nil && closeErr == nil {
					closeErr = err
				}
			}

			s.resetPendingTranscriptionLocked()
			s.partialTranscriptBuilder.Reset()
			s.mu.Unlock()
		}

		// Protected by writeQueueMu, so no send-on-closed-channel race.
		s.closeWriteQueue()

		// Drain all queued outbound messages before closing socket.
		s.writeWg.Wait()

		if s.transcriptionSession != nil && s.transcriptionSession.conn != nil {
			_ = s.transcriptionSession.conn.Close()
		}

		s.readWg.Wait()
		s.eventWg.Wait()
	})

	return closeErr
}

func (s *openaiRealtimeStream) SetProperty(key string, value string) error {
	return nil
}

func (s *openaiRealtimeStream) Results() <-chan *insights.TranscriptionEvent {
	return s.results
}

func (s *openaiRealtimeStream) readTranscriptionLoop() {
	defer s.readWg.Done()

	for {
		select {
		case <-s.ctx.Done():
			s.log.Infoln("context canceled, stopping openai transcription read loop")
			s.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
			return

		default:
			if s.transcriptionSession == nil || s.transcriptionSession.conn == nil {
				s.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
				return
			}

			_, data, err := s.transcriptionSession.conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					s.log.Infoln("openai realtime transcription connection closed normally")
				} else if s.ctx.Err() == nil && !s.closed.Load() {
					s.log.WithError(err).Error("error reading from openai realtime transcription")
					s.safeSend(&insights.TranscriptionEvent{
						Type:  insights.EventTypeError,
						Error: err.Error(),
					})
				}

				s.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
				return
			}

			s.handleTranscriptionMessage(data)
		}
	}
}

func (s *openaiRealtimeStream) parseEvent(data []byte) (*openaiRealtimeEvent, error) {
	var msg openaiRealtimeEvent
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}

func (s *openaiRealtimeStream) handleTranscriptionMessage(data []byte) {
	msg, err := s.parseEvent(data)
	if err != nil {
		s.log.WithError(err).Error("failed to unmarshal openai transcription message")
		return
	}

	switch msg.Type {
	case string(constant.ValueOf[constant.ConversationItemInputAudioTranscriptionDelta]()):
		if msg.Delta == "" {
			return
		}

		s.mu.Lock()
		s.partialTranscriptBuilder.WriteString(msg.Delta)
		partialText := s.partialTranscriptBuilder.String()
		s.mu.Unlock()

		if strings.TrimSpace(partialText) == "" {
			return
		}

		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:   s.opts.UserName,
			FromUserName: s.opts.UserName,
			Lang:         s.opts.SpokenLang,
			Text:         partialText,
			IsPartial:    true,
		}

		s.safeSend(&insights.TranscriptionEvent{
			Type:   insights.EventTypePartialResult,
			Result: result,
		})

	case string(constant.ValueOf[constant.ConversationItemInputAudioTranscriptionCompleted]()):
		finalText := strings.TrimSpace(msg.Transcript)
		if finalText == "" {
			return
		}

		s.mu.Lock()
		s.partialTranscriptBuilder.Reset()
		s.mu.Unlock()

		s.dispatchFinalTranscriptionAsync(finalText)

	case string(constant.ValueOf[constant.Error]()):
		s.mu.Lock()
		s.partialTranscriptBuilder.Reset()
		s.mu.Unlock()

		s.log.Errorln("openai realtime transcription error:", msg.Error.Message)
		s.safeSend(&insights.TranscriptionEvent{
			Type:  insights.EventTypeError,
			Error: msg.Error.Message,
		})

	default:
		return
	}
}

func (s *openaiRealtimeStream) dispatchFinalTranscriptionAsync(finalText string) {
	s.eventWg.Add(1)

	go func() {
		defer s.eventWg.Done()

		result := &plugnmeet.InsightsTranscriptionResult{
			FromUserId:                  s.opts.UserName,
			FromUserName:                s.opts.UserName,
			Lang:                        s.opts.SpokenLang,
			Text:                        finalText,
			IsPartial:                   false,
			AllowedTranscriptionStorage: s.opts.AllowedTranscriptionStorage,
			Translations:                make(map[string]string),
		}

		if len(s.opts.TransLangs) > 0 && s.llmProvider != nil {
			translationResult, err := s.llmProvider.TranslateText(
				s.ctx,
				finalText,
				s.opts.SpokenLang,
				s.opts.TransLangs,
			)
			if err != nil {
				if s.ctx.Err() == nil && !s.closed.Load() {
					s.log.WithError(err).Error("failed to translate final transcript")
				}
			} else if translationResult != nil {
				result.Translations = translationResult.Translations
			}
		}

		s.safeSend(&insights.TranscriptionEvent{
			Type:   insights.EventTypeFinalResult,
			Result: result,
		})
	}()
}

func (s *openaiRealtimeStream) resetPendingTranscriptionLocked() {
	s.transcriptionPendingPCM = s.transcriptionPendingPCM[:0]
	s.transcriptionSegmentSamples = 0
	s.transcriptionSegmentStartedAt = time.Time{}
	s.transcriptionLastSpeechAt = time.Time{}
	s.transcriptionHasSpeech = false
}

func (s *openaiRealtimeStream) safeSend(event *insights.TranscriptionEvent) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Warnln("could not send to resultsChan, likely closed:", r)
		}
	}()

	select {
	case <-s.ctx.Done():
		return
	case s.results <- event:
	}
}

func pcm16SampleToInt16(sample media.PCM16Sample) []int16 {
	out := make([]int16, len(sample))
	for i, v := range sample {
		out[i] = v
	}
	return out
}

func pcm16ToBytes(sample []int16) []byte {
	byteSlice := make([]byte, len(sample)*2)
	for i, val := range sample {
		binary.LittleEndian.PutUint16(byteSlice[i*2:], uint16(val))
	}

	return byteSlice
}

func hasSpeechPCM16(pcm []int16, threshold float64) bool {
	if len(pcm) == 0 {
		return false
	}

	if threshold <= 0 {
		threshold = float64(defaultTranscriptionSpeechRMS)
	}

	var sum float64
	for _, v := range pcm {
		f := float64(v)
		sum += f * f
	}

	rms := math.Sqrt(sum / float64(len(pcm)))

	return rms >= threshold
}

func resamplePCM16Linear(input []int16, inputRate, outputRate int) []int16 {
	if len(input) == 0 || inputRate <= 0 || outputRate <= 0 || inputRate == outputRate {
		return input
	}

	outputLen := int(math.Round(float64(len(input)) * float64(outputRate) / float64(inputRate)))
	if outputLen <= 0 {
		return nil
	}

	if len(input) == 1 {
		out := make([]int16, outputLen)
		for i := range out {
			out[i] = input[0]
		}
		return out
	}

	out := make([]int16, outputLen)
	ratio := float64(inputRate) / float64(outputRate)

	for i := 0; i < outputLen; i++ {
		srcPos := float64(i) * ratio
		srcIdx := int(math.Floor(srcPos))
		frac := srcPos - float64(srcIdx)

		if srcIdx >= len(input)-1 {
			out[i] = input[len(input)-1]
			continue
		}

		a := float64(input[srcIdx])
		b := float64(input[srcIdx+1])
		value := a + (b-a)*frac

		if value > math.MaxInt16 {
			value = math.MaxInt16
		} else if value < math.MinInt16 {
			value = math.MinInt16
		}

		out[i] = int16(math.Round(value))
	}

	return out
}
