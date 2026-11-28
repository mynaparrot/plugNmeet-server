package avatar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

const (
	// wsAgentSampleRate is the rate of the PCM the agent hands to Write.
	wsAgentSampleRate = media.DefaultOutputSampleRate24k

	// wsFirstChunkBytes and wsSubsequentChunkBytes pace the audio. The first
	// chunk of a segment goes out at 0.6s so the avatar starts talking with less
	// latency, the following ones are a full second of audio.
	wsFirstChunkBytes      = wsAgentSampleRate * 2 * 6 / 10
	wsSubsequentChunkBytes = wsAgentSampleRate * 2

	// wsKeepAliveInterval keeps an otherwise idle session open.
	wsKeepAliveInterval = 60 * time.Second
	// wsWriteTimeout bounds a single websocket write.
	wsWriteTimeout = 10 * time.Second
)

// wsSessionConfig describes how to open a session websocket and how to end it.
type wsSessionConfig struct {
	provider   config.ProviderType
	sessionID  string
	sampleRate int
	wsURL      string
	headers    map[string]string
	// initPayload is sent right after the connection opens, for a host that
	// pointed the provider at a socket that takes the session config over the
	// wire instead of a REST call.
	initPayload any
	// waitConnected waits for the avatar to report the session as connected
	// before the session is handed out.
	waitConnected bool
	closeFn       func(ctx context.Context) error
	dialContext   func(ctx context.Context, network, addr string) (net.Conn, error)
}

// wsSession is an avatar session that takes the agent audio over a websocket:
// AgentSpeak/AgentSpeakEnd events carry the speech, the incoming events report
// what the avatar did with it.
type wsSession struct {
	provider   config.ProviderType
	sessionID  string
	sampleRate int

	conn    *websocket.Conn
	writeMu sync.Mutex

	mu           sync.Mutex
	buf          []byte
	firstChunk   bool
	listening    bool
	listeningSet bool
	closed       bool

	playout media.PlayoutTracker
	// lastWriteUnixNano is when a message was last written, so an idle socket
	// is kept alive while a busy one is not pinged needlessly.
	lastWriteUnixNano atomic.Int64

	connectedOnce sync.Once
	connectedCh   chan struct{}
	done          chan struct{}

	closeOnce sync.Once
	closeFn   func(ctx context.Context) error
}

// newWSSession dials the session websocket and starts its read and keep-alive
// loops.
func newWSSession(ctx context.Context, cfg wsSessionConfig) (*wsSession, error) {
	dialer := websocket.Dialer{HandshakeTimeout: defaultHTTPTimeout}
	if cfg.dialContext != nil {
		dialer.NetDialContext = cfg.dialContext
	}

	header := http.Header{}
	for key, value := range cfg.headers {
		header.Set(key, value)
	}

	conn, resp, err := dialer.DialContext(ctx, cfg.wsURL, header)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("failed to connect to the avatar session: %w", err)
	}

	s := &wsSession{
		provider:    cfg.provider,
		sessionID:   cfg.sessionID,
		sampleRate:  normalizeWSSampleRate(cfg.sampleRate),
		conn:        conn,
		firstChunk:  true,
		connectedCh: make(chan struct{}),
		done:        make(chan struct{}),
		closeFn:     cfg.closeFn,
	}

	go s.readLoop()
	go s.keepAliveLoop()

	if cfg.initPayload != nil {
		if err := s.sendJSON(cfg.initPayload); err != nil {
			s.closeFn = nil
			_ = s.Close(context.Background())
			return nil, fmt.Errorf("failed to initialize the avatar session: %w", err)
		}
		s.markConnected()
	}
	if cfg.waitConnected {
		s.awaitConnected(ctx)
	}

	return s, nil
}

// normalizeWSSampleRate keeps the rate the agent audio is streamed at.
func normalizeWSSampleRate(sampleRate int) int {
	if sampleRate == media.DefaultSampleRate16k {
		return media.DefaultSampleRate16k
	}

	return media.DefaultOutputSampleRate24k
}

func (s *wsSession) Provider() config.ProviderType { return s.provider }
func (s *wsSession) SessionID() string             { return s.sessionID }
func (s *wsSession) SampleRate() int               { return s.sampleRate }

// AudioPublisher exposes the audio side of the session. The agent needs a no-arg
// Close, which cannot share the name with Close(ctx) of Session.
func (s *wsSession) AudioPublisher() AudioPublisher { return &wsAudioPublisher{session: s} }

// awaitConnected waits for the session to report itself connected, for at most
// one provider call. A server that never reports it is not an error: the socket
// buffers the audio anyway.
func (s *wsSession) awaitConnected(ctx context.Context) {
	timer := time.NewTimer(defaultHTTPTimeout)
	defer timer.Stop()

	select {
	case <-s.connectedCh:
	case <-s.done:
	case <-ctx.Done():
	case <-timer.C:
	}
}

// Write accepts 24 kHz 16-bit mono PCM and sends it to the avatar in chunks, so
// the first chunk of a segment starts the speech early and the rest follows at a
// steady pace.
func (s *wsSession) Write(pcm24k []byte) (int, error) {
	if len(pcm24k) == 0 {
		return 0, nil
	}
	// A write proves the audio path works, so a missing state update matters no
	// further.
	s.markConnected()

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, errors.New("avatar session is closed")
	}
	s.playout.Advance(len(pcm24k)/2, wsAgentSampleRate)
	s.buf = append(s.buf, pcm24k...)

	threshold := wsSubsequentChunkBytes
	if s.firstChunk {
		threshold = wsFirstChunkBytes
	}
	if len(s.buf) < threshold {
		s.mu.Unlock()
		return len(pcm24k), nil
	}
	chunk := s.buf
	s.buf = nil
	s.firstChunk = false
	s.mu.Unlock()

	if err := s.speak(chunk); err != nil {
		return 0, err
	}

	return len(pcm24k), nil
}

// EndSegment flushes the tail of the utterance, closes the segment and lets the
// avatar listen again.
func (s *wsSession) EndSegment() {
	s.mu.Lock()
	chunk := s.buf
	s.buf = nil
	s.firstChunk = true
	s.mu.Unlock()

	if len(chunk) > 0 {
		_ = s.speak(chunk)
	}
	_ = s.sendJSON(map[string]any{"type": "agent.speak_end", "event_id": uuid.NewString()})
	s.SetListening(true)
}

// Flush drops the audio that was written but not played yet, for a barge-in.
func (s *wsSession) Flush() {
	s.mu.Lock()
	s.buf = nil
	s.firstChunk = true
	s.mu.Unlock()
	s.playout.Reset()

	_ = s.sendJSON(map[string]any{"type": "agent.interrupt", "event_id": uuid.NewString()})
}

// SetListening tells the avatar whether the agent listens to the room; a repeat
// of the current value is dropped.
func (s *wsSession) SetListening(listening bool) {
	s.mu.Lock()
	if s.closed || (s.listeningSet && s.listening == listening) {
		s.mu.Unlock()
		return
	}
	s.listening = listening
	s.listeningSet = true
	s.mu.Unlock()

	messageType := "agent.stop_listening"
	if listening {
		messageType = "agent.start_listening"
	}
	_ = s.sendJSON(map[string]any{"type": messageType, "event_id": uuid.NewString()})
}

// RemainingDuration reports how much of the written audio has not played yet.
func (s *wsSession) RemainingDuration() time.Duration {
	return s.playout.Remaining()
}

// Close ends the session on the avatar side and closes the socket. It is safe to
// call more than once.
func (s *wsSession) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()

		close(s.done)
		_ = s.conn.Close()

		if s.closeFn != nil {
			err = s.closeFn(ctx)
		}
	})

	return err
}

// speak sends one chunk of PCM as an agent.speak event.
func (s *wsSession) speak(pcm []byte) error {
	payload := pcm
	if s.sampleRate == media.DefaultSampleRate16k {
		payload = media.ResamplePCM16Mono24kTo16k(pcm)
	}

	return s.sendJSON(map[string]any{
		"type":     "agent.speak",
		"event_id": uuid.NewString(),
		"audio":    base64.StdEncoding.EncodeToString(payload),
	})
}

// sendJSON marshals payload and writes it as a text message.
func (s *wsSession) sendJSON(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal the session message: %w", err)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	now := time.Now()
	_ = s.conn.SetWriteDeadline(now.Add(wsWriteTimeout))
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("failed to send the session message: %w", err)
	}
	s.lastWriteUnixNano.Store(now.UnixNano())

	return nil
}

// wsIncomingMessage is the subset of the incoming events the session reacts to.
type wsIncomingMessage struct {
	Type  string `json:"type"`
	State string `json:"state"`
}

func (s *wsSession) readLoop() {
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		incoming := wsIncomingMessage{}
		if json.Unmarshal(data, &incoming) != nil {
			continue
		}
		s.handleMessage(incoming)
	}
}

func (s *wsSession) handleMessage(msg wsIncomingMessage) {
	switch msg.Type {
	case "session.state_updated":
		if msg.State == "connected" {
			s.markConnected()
		}
	case "agent.state_updated":
		s.markConnected()
	case "agent.speak_ended":
		// The avatar played what it had. Buffered chunks still to be sent mean
		// the segment is not over, so the playout stays.
		s.mu.Lock()
		buffered := len(s.buf) > 0
		s.mu.Unlock()
		if !buffered {
			s.playout.Reset()
		}
	case "agent.speak_interrupted", "agent.audio_buffer_cleared":
		s.playout.Reset()
	}
}

func (s *wsSession) markConnected() {
	s.connectedOnce.Do(func() { close(s.connectedCh) })
}

func (s *wsSession) keepAliveLoop() {
	ticker := time.NewTicker(wsKeepAliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			// A session that wrote within the interval is not idle.
			if time.Since(time.Unix(0, s.lastWriteUnixNano.Load())) < wsKeepAliveInterval {
				continue
			}
			_ = s.sendJSON(map[string]any{"type": "session.keep_alive", "event_id": uuid.NewString()})
		}
	}
}

// wsAudioPublisher is the audio side of a wsSession, as the agent consumes it.
type wsAudioPublisher struct {
	session *wsSession
}

func (p *wsAudioPublisher) Write(data []byte) (int, error) { return p.session.Write(data) }
func (p *wsAudioPublisher) EndSegment()                    { p.session.EndSegment() }
func (p *wsAudioPublisher) Flush()                         { p.session.Flush() }
func (p *wsAudioPublisher) RemainingDuration() time.Duration {
	return p.session.RemainingDuration()
}
func (p *wsAudioPublisher) Close() { _ = p.session.Close(context.Background()) }
