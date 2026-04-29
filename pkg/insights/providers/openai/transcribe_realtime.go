package openai

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/media-sdk"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// realtimeStream implements insights.TranscriptionStream over the OpenAI
// Realtime WebSocket API, surfacing per-segment partial_result and
// final_result events. Requires a backend that speaks the Realtime protocol
// (OpenAI cloud or Azure OpenAI Realtime).
type realtimeStream struct {
	conn     *websocket.Conn
	model    string
	language string
	userId   string
	userName string
	allow    bool

	// All sends go through outbound; gorilla/websocket forbids concurrent writes.
	outbound chan []byte
	results  chan *insights.TranscriptionEvent

	pmu      sync.Mutex
	partials map[string]string

	closed atomic.Bool
	wg     sync.WaitGroup
	cancel context.CancelFunc
	log    *logrus.Entry
}

const (
	defaultRealtimeURL   = "wss://api.openai.com/v1/realtime?intent=transcription"
	defaultRealtimeModel = "gpt-4o-mini-transcribe"
	realtimeDialTimeout  = 10 * time.Second
)

func newRealtimeStream(parentCtx context.Context, account *config.ProviderAccount, model, roomId, userId string, opts *insights.TranscriptionOptions, log *logrus.Entry) (*realtimeStream, error) {
	if account == nil {
		return nil, fmt.Errorf("openai realtime: provider account is nil")
	}
	if account.Credentials.APIKey == "" {
		return nil, fmt.Errorf("openai realtime: credentials.api_key is required")
	}

	url := defaultRealtimeURL
	if v, _ := account.Options["realtime_url"].(string); v != "" {
		url = v
	}
	// whisper-1 is rejected by the Realtime transcription endpoint.
	if model == "" || model == defaultTranscriptionModel {
		model = defaultRealtimeModel
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+account.Credentials.APIKey)
	header.Set("OpenAI-Beta", "realtime=v1")

	dialer := &websocket.Dialer{HandshakeTimeout: realtimeDialTimeout}
	dialCtx, dialCancel := context.WithTimeout(parentCtx, realtimeDialTimeout)
	defer dialCancel()

	conn, resp, err := dialer.DialContext(dialCtx, url, header)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("openai realtime: dial %s: %w (http %d)", url, err, resp.StatusCode)
		}
		return nil, fmt.Errorf("openai realtime: dial %s: %w", url, err)
	}

	ctx, cancel := context.WithCancel(parentCtx)
	s := &realtimeStream{
		conn:     conn,
		model:    model,
		language: opts.SpokenLang,
		userId:   userId,
		userName: opts.UserName,
		allow:    opts.AllowedTranscriptionStorage,
		outbound: make(chan []byte, 64),
		results:  make(chan *insights.TranscriptionEvent, 16),
		partials: make(map[string]string),
		cancel:   cancel,
		log: log.WithFields(logrus.Fields{
			"roomId": roomId,
			"userId": userId,
			"lang":   opts.SpokenLang,
			"model":  model,
			"mode":   modeRealtime,
		}),
	}

	if err := s.sendSessionUpdate(); err != nil {
		_ = conn.Close()
		cancel()
		return nil, err
	}

	s.wg.Add(2)
	go s.writeLoop(ctx)
	go s.readLoop(ctx)

	s.results <- &insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted}
	return s, nil
}

// sendSessionUpdate runs synchronously before writeLoop starts, so it writes
// directly. After that, all sends MUST go through outbound.
func (s *realtimeStream) sendSessionUpdate() error {
	session := map[string]any{
		"type": "transcription_session.update",
		"session": map[string]any{
			"input_audio_format": "pcm16",
			"input_audio_transcription": map[string]any{
				"model":    s.model,
				"language": s.language,
			},
			// Server VAD drives the .completed event we map to final_result.
			"turn_detection": map[string]any{
				"type":                "server_vad",
				"threshold":           0.5,
				"prefix_padding_ms":   300,
				"silence_duration_ms": 500,
			},
		},
	}
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("openai realtime: marshal session.update: %w", err)
	}
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return fmt.Errorf("openai realtime: send session.update: %w", err)
	}
	return nil
}

func (s *realtimeStream) WriteSample(sample media.PCM16Sample) error {
	if s.closed.Load() {
		return fmt.Errorf("stream is closed")
	}
	if len(sample) == 0 {
		return nil
	}

	raw := make([]byte, len(sample)*2)
	for i, v := range sample {
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(v))
	}

	event := map[string]string{
		"type":  "input_audio_buffer.append",
		"audio": base64.StdEncoding.EncodeToString(raw),
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("openai realtime: marshal append: %w", err)
	}

	select {
	case s.outbound <- payload:
	default:
		s.log.Warn("openai realtime outbound queue full, dropping audio frame")
	}
	return nil
}

func (s *realtimeStream) SetProperty(_, _ string) error { return nil }

func (s *realtimeStream) Results() <-chan *insights.TranscriptionEvent { return s.results }

func (s *realtimeStream) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}

	commit, _ := json.Marshal(map[string]string{"type": "input_audio_buffer.commit"})
	select {
	case s.outbound <- commit:
	default:
	}

	close(s.outbound)
	s.cancel()
	// Close unblocks readLoop with a network error.
	_ = s.conn.Close()

	s.wg.Wait()
	close(s.results)
	return nil
}

func (s *realtimeStream) writeLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.outbound:
			if !ok {
				return
			}
			_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := s.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				if !s.closed.Load() {
					s.log.WithError(err).Warn("openai realtime write failed")
					s.safeSend(&insights.TranscriptionEvent{
						Type:  insights.EventTypeError,
						Error: err.Error(),
					})
				}
				return
			}
		}
	}
}

func (s *realtimeStream) readLoop(ctx context.Context) {
	defer s.wg.Done()
	defer s.safeSend(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})

	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil || s.closed.Load() {
				return
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			s.log.WithError(err).Warn("openai realtime read failed")
			s.safeSend(&insights.TranscriptionEvent{
				Type:  insights.EventTypeError,
				Error: err.Error(),
			})
			return
		}
		s.dispatch(data)
	}
}

type realtimeServerEvent struct {
	Type       string `json:"type"`
	ItemID     string `json:"item_id,omitempty"`
	Delta      string `json:"delta,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Error      *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

func (s *realtimeStream) dispatch(data []byte) {
	var ev realtimeServerEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		s.log.WithError(err).Debug("openai realtime: unparseable server event")
		return
	}

	switch ev.Type {
	case "conversation.item.input_audio_transcription.delta":
		text := s.appendPartial(ev.ItemID, ev.Delta)
		if text == "" {
			return
		}
		s.safeSend(&insights.TranscriptionEvent{
			Type:   insights.EventTypePartialResult,
			Result: s.buildResult(text, true),
		})

	case "conversation.item.input_audio_transcription.completed":
		text := strings.TrimSpace(ev.Transcript)
		s.clearPartial(ev.ItemID)
		if text == "" {
			return
		}
		s.safeSend(&insights.TranscriptionEvent{
			Type:   insights.EventTypeFinalResult,
			Result: s.buildResult(text, false),
		})

	case "error":
		msg := "unknown realtime error"
		if ev.Error != nil && ev.Error.Message != "" {
			msg = ev.Error.Message
		}
		s.safeSend(&insights.TranscriptionEvent{
			Type:  insights.EventTypeError,
			Error: msg,
		})
	}
}

func (s *realtimeStream) appendPartial(itemID, delta string) string {
	if itemID == "" || delta == "" {
		return ""
	}
	s.pmu.Lock()
	defer s.pmu.Unlock()
	s.partials[itemID] += delta
	return strings.TrimSpace(s.partials[itemID])
}

func (s *realtimeStream) clearPartial(itemID string) {
	if itemID == "" {
		return
	}
	s.pmu.Lock()
	delete(s.partials, itemID)
	s.pmu.Unlock()
}

func (s *realtimeStream) buildResult(text string, isPartial bool) *plugnmeet.InsightsTranscriptionResult {
	return &plugnmeet.InsightsTranscriptionResult{
		FromUserId:                  s.userId,
		FromUserName:                s.userName,
		Lang:                        s.language,
		Text:                        text,
		IsPartial:                   isPartial,
		AllowedTranscriptionStorage: s.allow,
		Translations:                map[string]string{},
	}
}

func (s *realtimeStream) safeSend(ev *insights.TranscriptionEvent) {
	defer func() { _ = recover() }()
	select {
	case s.results <- ev:
	default:
		s.log.Warn("openai realtime results channel full, dropping event")
	}
}
