package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

func testLog() *logrus.Entry {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return logrus.NewEntry(l)
}

// realtimeTestServer records client messages and lets a test script replies.
type realtimeTestServer struct {
	t        *testing.T
	srv      *httptest.Server
	messages chan map[string]any
	pings    atomic.Int32

	mu       sync.Mutex
	conn     *websocket.Conn
	onClient func(conn *websocket.Conn, msg map[string]any)
}

func newRealtimeTestServer(t *testing.T) *realtimeTestServer {
	t.Helper()

	s := &realtimeTestServer{
		t:        t,
		messages: make(chan map[string]any, 128),
	}

	upgrader := websocket.Upgrader{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		conn.SetPingHandler(func(data string) error {
			s.pings.Add(1)
			return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})

		s.mu.Lock()
		s.conn = conn
		onClient := s.onClient
		s.mu.Unlock()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			msg := map[string]any{}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			s.messages <- msg
			if onClient != nil {
				onClient(conn, msg)
			}
		}
	}))

	t.Cleanup(s.srv.Close)
	return s
}

func (s *realtimeTestServer) setOnClient(fn func(conn *websocket.Conn, msg map[string]any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClient = fn
}

func (s *realtimeTestServer) send(payload string) {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		s.t.Error("no client connected")
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		s.t.Logf("test server write failed: %v", err)
	}
}

func (s *realtimeTestServer) closeClient() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (s *realtimeTestServer) nextMessage(within time.Duration) (map[string]any, bool) {
	s.t.Helper()

	select {
	case msg := <-s.messages:
		return msg, true
	case <-time.After(within):
		return nil, false
	}
}

func newTestRealtimeStream(t *testing.T, serverURL string, options map[string]interface{}) (insights.TranscriptionStream, *realtimeClient) {
	t.Helper()

	account := &config.ProviderAccount{
		Options: map[string]interface{}{"endpoint": serverURL},
	}
	service := &config.ServiceConfig{Options: options}

	rc, err := newRealtimeClient(account, service, testLog(), nil)
	if err != nil {
		t.Fatalf("newRealtimeClient: %v", err)
	}

	stream, err := rc.CreateTranscription(context.Background(), "room-1", "user-1", &insights.TranscriptionOptions{
		SpokenLang: "en",
	})
	if err != nil {
		t.Fatalf("CreateTranscription: %v", err)
	}

	return stream, rc
}

func waitEvent(t *testing.T, stream insights.TranscriptionStream, want insights.EventType, within time.Duration) *insights.TranscriptionEvent {
	t.Helper()

	deadline := time.After(within)
	for {
		select {
		case event, ok := <-stream.Results():
			if !ok {
				t.Fatalf("results closed while waiting for %s", want)
			}
			if event.Type == want {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for event %s", want)
		}
	}
}

func noEvent(t *testing.T, stream insights.TranscriptionStream, unwanted insights.EventType, within time.Duration) {
	t.Helper()

	deadline := time.After(within)
	for {
		select {
		case event, ok := <-stream.Results():
			if !ok {
				return
			}
			if event.Type == unwanted {
				t.Fatalf("unexpected event %s", unwanted)
			}
		case <-deadline:
			return
		}
	}
}

func closeWithin(t *testing.T, stream insights.TranscriptionStream, within time.Duration) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		_ = stream.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(within):
		t.Fatal("stream Close did not return in time")
	}
}

// The session must be configured before any audio is sent, and the stream is
// only ready once the server acknowledged the configuration.
func TestRealtimeTranscriptionRequiresSessionAck(t *testing.T) {
	srv := newRealtimeTestServer(t)
	stream, _ := newTestRealtimeStream(t, srv.srv.URL, map[string]interface{}{
		"transcription_turn_detection": "server_vad",
	})

	first, ok := srv.nextMessage(2 * time.Second)
	if !ok {
		t.Fatal("no session configuration was sent")
	}
	if first["type"] != "session.update" {
		t.Fatalf("expected session.update first, got %v", first["type"])
	}

	// An accepted dial is not proof that the configuration was accepted.
	noEvent(t, stream, insights.EventTypeSessionStarted, 100*time.Millisecond)

	srv.send(`{"type":"transcription_session.updated"}`)
	waitEvent(t, stream, insights.EventTypeSessionStarted, 2*time.Second)

	if err := stream.WriteSample(media.PCM16Sample{2000, 2000, 2000, 2000}); err != nil {
		t.Fatalf("WriteSample: %v", err)
	}

	msg, ok := srv.nextMessage(2 * time.Second)
	if !ok || msg["type"] != "input_audio_buffer.append" {
		t.Fatalf("expected audio to be appended after the ack, got %v", msg["type"])
	}

	closeWithin(t, stream, 3*time.Second)
}

// A configuration/auth error before the ack must terminate the stream promptly.
func TestRealtimeTranscriptionConfigErrorBeforeAck(t *testing.T) {
	srv := newRealtimeTestServer(t)
	srv.setOnClient(func(conn *websocket.Conn, msg map[string]any) {
		if msg["type"] == "session.update" {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","error":{"message":"invalid model"}}`))
		}
	})

	stream, _ := newTestRealtimeStream(t, srv.srv.URL, nil)

	waitEvent(t, stream, insights.EventTypeError, 2*time.Second)
	waitEvent(t, stream, insights.EventTypeSessionStopped, 2*time.Second)

	closeWithin(t, stream, 3*time.Second)
}

// A full results queue must not be able to deadlock Close, which is what the
// task calls while tearing a stream down: closing cancels the context first, so
// the event senders unblock instead of waiting for a consumer that is gone.
func TestRealtimeTranscriptionCloseWithFullResultsQueue(t *testing.T) {
	srv := newRealtimeTestServer(t)
	srv.setOnClient(func(conn *websocket.Conn, msg map[string]any) {
		if msg["type"] != "session.update" {
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcription_session.updated"}`))
		go func() {
			for i := 0; i < 200; i++ {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"conversation.item.input_audio_transcription.delta","delta":"hello"}`)); err != nil {
					return
				}
			}
		}()
	})

	stream, _ := newTestRealtimeStream(t, srv.srv.URL, nil)
	waitEvent(t, stream, insights.EventTypeSessionStarted, 2*time.Second)

	// Nothing consumes the results from here on, so the channel fills up while
	// the read loop is blocked inside an event send.
	time.Sleep(300 * time.Millisecond)
	closeWithin(t, stream, 3*time.Second)
}

// Pings keep an idle session alive; a remote close still ends the stream.
func TestRealtimeTranscriptionPingKeepsSessionAlive(t *testing.T) {
	prevPing, prevWindow := realtimePingInterval, realtimeReadWindow
	realtimePingInterval = 10 * time.Millisecond
	realtimeReadWindow = 150 * time.Millisecond
	t.Cleanup(func() {
		realtimePingInterval, realtimeReadWindow = prevPing, prevWindow
	})

	srv := newRealtimeTestServer(t)
	srv.setOnClient(func(conn *websocket.Conn, msg map[string]any) {
		if msg["type"] == "session.update" {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcription_session.updated"}`))
		}
	})

	stream, _ := newTestRealtimeStream(t, srv.srv.URL, nil)

	waitEvent(t, stream, insights.EventTypeSessionStarted, 2*time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for srv.pings.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.pings.Load() == 0 {
		t.Fatal("client did not send websocket pings")
	}

	// Well past the read window: pongs must have kept the session open.
	noEvent(t, stream, insights.EventTypeSessionStopped, 400*time.Millisecond)

	srv.closeClient()
	waitEvent(t, stream, insights.EventTypeSessionStopped, 2*time.Second)

	closeWithin(t, stream, 3*time.Second)
}
