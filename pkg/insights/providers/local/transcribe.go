package local

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

type whisperMsg struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	Lang  string `json:"lang,omitempty"`
	Error string `json:"error,omitempty"`
}

// audioBufPool reuses PCM-encoding scratch buffers. WriteSample is called
// at audio-frame rate (tens of Hz per active speaker), so avoiding per-call
// allocation materially reduces GC pressure on busy servers. Initial cap is
// sized for typical LiveKit opus frame expansions.
var audioBufPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 0, 4096)
		return &b
	},
}

type localTranscribeStream struct {
	conn    *websocket.Conn
	cancel  context.CancelFunc
	results chan *insights.TranscriptionEvent
	mu      sync.Mutex
	closed  bool
	opts    *insights.TranscriptionOptions
	userId  string
	log     *logrus.Entry
}

func newTranscribeStream(mainCtx context.Context, wsURL, roomId, userId string, opts *insights.TranscriptionOptions, log *logrus.Entry) (*localTranscribeStream, error) {
	l := log.WithFields(logrus.Fields{
		"roomId": roomId,
		"userId": userId,
		"lang":   opts.SpokenLang,
	})

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to whisper service at %s: %w", wsURL, err)
	}

	// Send handshake so the server knows the language and target translations.
	handshake := map[string]interface{}{
		"type":       "start",
		"lang":       opts.SpokenLang,
		"transLangs": opts.TransLangs,
		"userName":   opts.UserName,
	}
	if err := conn.WriteJSON(handshake); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send handshake to whisper service: %w", err)
	}

	resultsChan := make(chan *insights.TranscriptionEvent, 32)
	ctx, cancel := context.WithCancel(mainCtx)

	s := &localTranscribeStream{
		conn:    conn,
		cancel:  cancel,
		results: resultsChan,
		opts:    opts,
		userId:  userId,
		log:     l,
	}

	go s.readLoop(ctx, resultsChan)

	return s, nil
}

func (s *localTranscribeStream) readLoop(ctx context.Context, resultsChan chan *insights.TranscriptionEvent) {
	safeClose := sync.OnceFunc(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(resultsChan)
	})
	defer safeClose()

	// emit sends an event or aborts if the context is done. Returning false
	// tells the caller to unwind — the consumer is gone and further sends
	// would block forever on a full channel.
	emit := func(ev *insights.TranscriptionEvent) bool {
		select {
		case resultsChan <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	if !emit(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted}) {
		return
	}

	for {
		select {
		case <-ctx.Done():
			emit(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
			return
		default:
		}

		_, msgBytes, err := s.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				s.log.WithError(err).Warn("whisper WebSocket read error")
				if !emit(&insights.TranscriptionEvent{
					Type:  insights.EventTypeError,
					Error: err.Error(),
				}) {
					return
				}
			}
			emit(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
			return
		}

		var msg whisperMsg
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			s.log.WithError(err).Warn("failed to parse whisper message")
			continue
		}

		switch msg.Type {
		case "partial":
			if !emit(&insights.TranscriptionEvent{
				Type: insights.EventTypePartialResult,
				Result: &plugnmeet.InsightsTranscriptionResult{
					FromUserId:                  s.userId,
					FromUserName:                s.opts.UserName,
					Lang:                        msg.Lang,
					Text:                        msg.Text,
					IsPartial:                   true,
					AllowedTranscriptionStorage: s.opts.AllowedTranscriptionStorage,
					Translations:                make(map[string]string),
				},
			}) {
				return
			}
		case "final":
			if !emit(&insights.TranscriptionEvent{
				Type: insights.EventTypeFinalResult,
				Result: &plugnmeet.InsightsTranscriptionResult{
					FromUserId:                  s.userId,
					FromUserName:                s.opts.UserName,
					Lang:                        msg.Lang,
					Text:                        msg.Text,
					IsPartial:                   false,
					AllowedTranscriptionStorage: s.opts.AllowedTranscriptionStorage,
					Translations:                make(map[string]string),
				},
			}) {
				return
			}
		case "translation":
			// Server can optionally send pre-translated results embedded in the message.
			// Handled separately if the whisper service does its own translation.
		case "error":
			if !emit(&insights.TranscriptionEvent{
				Type:  insights.EventTypeError,
				Error: msg.Error,
			}) {
				return
			}
		}
	}
}

// WriteSample converts PCM16 samples to bytes and sends them to the whisper service.
func (s *localTranscribeStream) WriteSample(sample media.PCM16Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("stream is closed")
	}

	need := len(sample) * 2
	bufPtr := audioBufPool.Get().(*[]byte)
	defer audioBufPool.Put(bufPtr)

	if cap(*bufPtr) < need {
		*bufPtr = make([]byte, need)
	} else {
		*bufPtr = (*bufPtr)[:need]
	}
	for i, v := range sample {
		binary.LittleEndian.PutUint16((*bufPtr)[i*2:], uint16(v))
	}

	// gorilla/websocket WriteMessage copies/consumes the buffer synchronously,
	// so returning it to the pool via defer is safe.
	return s.conn.WriteMessage(websocket.BinaryMessage, *bufPtr)
}

// Close signals the stream end, sends a WebSocket close frame, and cancels the context.
func (s *localTranscribeStream) Close() error {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		_ = s.conn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			time.Now().Add(2*time.Second),
		)
		return s.conn.Close()
	}
	return nil
}

// SetProperty is a no-op for this provider; all properties are set via the handshake.
func (s *localTranscribeStream) SetProperty(_, _ string) error {
	return nil
}

// Results returns the channel of transcription events.
func (s *localTranscribeStream) Results() <-chan *insights.TranscriptionEvent {
	return s.results
}
