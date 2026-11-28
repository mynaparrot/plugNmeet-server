package avatar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/media"
)

// wsTestMessage is the subset of the session protocol the tests assert on.
type wsTestMessage struct {
	Type        string `json:"type"`
	EventID     string `json:"event_id"`
	Audio       string `json:"audio"`
	AvatarID    string `json:"avatar_id"`
	LiveKitRoom string `json:"livekit_room"`
	SampleRate  int    `json:"sample_rate"`
}

// wsTestServer is a fake avatar server that records what a session sends.
type wsTestServer struct {
	srv      *httptest.Server
	messages chan wsTestMessage
	ready    chan struct{}
	readyOne sync.Once

	mu     sync.Mutex
	conn   *websocket.Conn
	header http.Header
}

// newWSTestServer starts a fake avatar server. autoConnect reports the session as
// connected right after the websocket is up, like a real provider does.
func newWSTestServer(t *testing.T, autoConnect bool) *wsTestServer {
	t.Helper()

	s := &wsTestServer{
		messages: make(chan wsTestMessage, 32),
		ready:    make(chan struct{}),
	}
	upgrader := websocket.Upgrader{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		s.mu.Lock()
		s.conn, s.header = conn, r.Header.Clone()
		s.mu.Unlock()
		s.readyOne.Do(func() { close(s.ready) })

		if autoConnect {
			_ = conn.WriteJSON(map[string]any{"type": "session.state_updated", "state": "connected"})
		}

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			message := wsTestMessage{}
			if json.Unmarshal(data, &message) == nil {
				s.messages <- message
			}
		}
	}))

	t.Cleanup(func() {
		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		s.srv.Close()
	})

	return s
}

// url is the websocket address of the fake server.
func (s *wsTestServer) url() string { return "ws" + strings.TrimPrefix(s.srv.URL, "http") }

func (s *wsTestServer) awaitConn(t *testing.T) *websocket.Conn {
	t.Helper()

	select {
	case <-s.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("the avatar session did not connect")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.conn
}

func (s *wsTestServer) headerValue(t *testing.T, name string) string {
	t.Helper()
	s.awaitConn(t)

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.header.Get(name)
}

// send pushes a message to the connected session.
func (s *wsTestServer) send(t *testing.T, payload map[string]any) {
	t.Helper()
	if err := s.awaitConn(t).WriteJSON(payload); err != nil {
		t.Fatalf("failed to write to the avatar session: %v", err)
	}
}

func (s *wsTestServer) nextMessage(t *testing.T) wsTestMessage {
	t.Helper()

	select {
	case message := <-s.messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a session message")
	}

	return wsTestMessage{}
}

// waitMessage returns the first message of the given type, skipping the others.
func (s *wsTestServer) waitMessage(t *testing.T, messageType string) wsTestMessage {
	t.Helper()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case message := <-s.messages:
			if message.Type == messageType {
				return message
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %q", messageType)
		}
	}
}

func (s *wsTestServer) noMessage(t *testing.T, wait time.Duration) {
	t.Helper()

	select {
	case message := <-s.messages:
		t.Fatalf("unexpected message %q", message.Type)
	case <-time.After(wait):
	}
}

func decodeAudio(t *testing.T, message wsTestMessage) []byte {
	t.Helper()

	audio, err := base64.StdEncoding.DecodeString(message.Audio)
	if err != nil {
		t.Fatalf("failed to decode the audio of %q: %v", message.Type, err)
	}

	return audio
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition was not met in time")
}

// newWSTestSession opens a session against the fake server.
func newWSTestSession(t *testing.T, srv *wsTestServer, cfg wsSessionConfig) *wsSession {
	t.Helper()

	cfg.wsURL = srv.url()
	session, err := newWSSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newWSSession returned error: %v", err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	return session
}

func TestStartSessionHeyGen(t *testing.T) {
	wsSrv := newWSTestServer(t, true)

	var tokenKey, startAuth, stopAuth string
	var tokenBody, startBody, stopBody map[string]any

	restSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			tokenKey = r.Header.Get("X-API-KEY")
			tokenBody = decodeBody(t, r)
			_, _ = w.Write([]byte(`{"data":{"session_id":"heygen-1","session_token":"heygen-token"}}`))
		case "/start":
			startAuth = r.Header.Get("Authorization")
			startBody = decodeBody(t, r)
			_, _ = fmt.Fprintf(w, `{"data":{"session_id":"heygen-1","ws_url":%q}}`, wsSrv.url())
		case "/stop":
			stopAuth = r.Header.Get("Authorization")
			stopBody = decodeBody(t, r)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer restSrv.Close()

	cfg := newTestConfig(config.ProviderHeyGen, restSrv.URL)
	cfg.LiveKitRoom = "room-1"
	sess, err := StartSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if sess.Provider() != config.ProviderHeyGen || sess.SessionID() != "heygen-1" || sess.SampleRate() != media.DefaultOutputSampleRate24k {
		t.Errorf("session = %q/%q/%d, want heygen/heygen-1/24000", sess.Provider(), sess.SessionID(), sess.SampleRate())
	}
	if tokenKey != testAPIKey {
		t.Errorf("token api key = %q, want %q", tokenKey, testAPIKey)
	}
	if tokenBody["mode"] != "LITE" || tokenBody["avatar_id"] != testAvatarID {
		t.Errorf("token body = %+v, want LITE mode for the avatar", tokenBody)
	}
	liveKitConfig, _ := tokenBody["livekit_config"].(map[string]any)
	if liveKitConfig["livekit_room"] != "room-1" || liveKitConfig["livekit_url"] != testLiveKitURL ||
		liveKitConfig["livekit_client_token"] != testLiveKitToken {
		t.Errorf("livekit_config = %+v, want the room of the agent", liveKitConfig)
	}
	if startAuth != "Bearer heygen-token" || startBody["session_id"] != "heygen-1" {
		t.Errorf("start = %q/%+v, want the session token and id", startAuth, startBody)
	}

	// the session waits for the state update before it is handed out, so the
	// audio can be written right away
	writer, ok := sess.(*wsSession)
	if !ok {
		t.Fatalf("session = %T, want *wsSession", sess)
	}
	if _, err := writer.AudioPublisher().Write(make([]byte, wsFirstChunkBytes)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := len(decodeAudio(t, wsSrv.waitMessage(t, "agent.speak"))); got != wsFirstChunkBytes {
		t.Errorf("first chunk = %d bytes, want %d", got, wsFirstChunkBytes)
	}

	writer.AudioPublisher().Close()
	if stopAuth != "Bearer heygen-token" {
		t.Errorf("stop authorization = %q, want the session token", stopAuth)
	}
	if stopBody["session_id"] != "heygen-1" || stopBody["reason"] != heyGenStopReason {
		t.Errorf("stop body = %+v, want the session id and reason", stopBody)
	}
}

func TestStartSessionGenericWebSocketDirectEndpoint(t *testing.T) {
	wsSrv := newWSTestServer(t, false)

	cfg := newTestConfig(config.ProviderWebSocket, wsSrv.url())
	cfg.LiveKitRoom = "room-7"
	sess, err := StartSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}
	defer func() { _ = sess.Close(context.Background()) }()

	init := wsSrv.waitMessage(t, "session.init")
	if init.AvatarID != testAvatarID || init.LiveKitRoom != "room-7" || init.SampleRate != media.DefaultOutputSampleRate24k {
		t.Errorf("session.init = %+v, want the avatar, room and sample rate", init)
	}
	if got := wsSrv.headerValue(t, "Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("authorization = %q, want the bearer key", got)
	}
}

func TestStartSessionGenericWebSocketRESTBootstrap(t *testing.T) {
	wsSrv := newWSTestServer(t, true)

	var startAuth string
	var startBody, endBody map[string]any

	var restSrv *httptest.Server
	restSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/end":
			endBody = decodeBody(t, r)
			_, _ = w.Write([]byte(`{}`))
		default:
			startAuth = r.Header.Get("Authorization")
			startBody = decodeBody(t, r)
			_, _ = fmt.Fprintf(w, `{"data":{"conversation_id":"conv-1","ws_url":%q,"end_url":%q}}`, wsSrv.url(), restSrv.URL+"/end")
		}
	}))
	defer restSrv.Close()

	cfg := newTestConfig(config.ProviderWebSocket, restSrv.URL)
	cfg.LiveKitRoom = "room-2"
	sess, err := StartSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if sess.SessionID() != "conv-1" {
		t.Errorf("session id = %q, want the wrapped conversation id", sess.SessionID())
	}
	if startAuth != "Bearer "+testAPIKey {
		t.Errorf("authorization = %q, want the bearer key", startAuth)
	}
	if startBody["livekit_url"] != testLiveKitURL || startBody["livekit_token"] != testLiveKitToken ||
		startBody["livekit_room"] != "room-2" || startBody["sample_rate"] != float64(media.DefaultOutputSampleRate24k) {
		t.Errorf("start body = %+v, want the transport of the agent", startBody)
	}

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if endBody["session_id"] != "conv-1" {
		t.Errorf("end body = %+v, want the session id", endBody)
	}
}

// A LiveKit avatar worker takes the voice from lk.audio_stream, so the session
// must stay socket-free: no ws_url is asked for, and the agent falls back to the
// datastream publisher on its own.
func TestStartSessionGenericDataStream(t *testing.T) {
	var startBody, endBody map[string]any
	var restSrv *httptest.Server
	restSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/end" {
			endBody = decodeBody(t, r)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		startBody = decodeBody(t, r)
		_, _ = fmt.Fprintf(w, `{"task_id":"task-9","sample_rate":16000,"end_url":%q}`, restSrv.URL+"/end")
	}))
	defer restSrv.Close()

	cfg := newTestConfig(config.ProviderLiveKitDataStream, restSrv.URL)
	cfg.LiveKitRoom = "room-3"
	sess, err := StartSession(context.Background(), cfg)
	if err != nil {
		t.Fatalf("StartSession returned error: %v", err)
	}

	if sess.Provider() != config.ProviderLiveKitDataStream || sess.SessionID() != "task-9" || sess.SampleRate() != media.DefaultSampleRate16k {
		t.Errorf("session = %q/%q/%d, want livekit_datastream/task-9/16000", sess.Provider(), sess.SessionID(), sess.SampleRate())
	}
	if startBody["avatar_id"] != testAvatarID || startBody["livekit_room"] != "room-3" {
		t.Errorf("start body = %+v, want the avatar and the room", startBody)
	}
	if _, ok := sess.(interface{ AudioPublisher() AudioPublisher }); ok {
		t.Errorf("session = %T, want a session without an audio publisher", sess)
	}

	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if endBody["session_id"] != "task-9" {
		t.Errorf("end body = %+v, want the session id", endBody)
	}
}

// sampleRates are the session settings the audio tests run with.
var testWSSessionConfig = wsSessionConfig{
	provider:   config.ProviderWebSocket,
	sessionID:  "session-1",
	sampleRate: media.DefaultOutputSampleRate24k,
}

func TestWSSessionChunksAndEndsSegment(t *testing.T) {
	wsSrv := newWSTestServer(t, false)
	session := newWSTestSession(t, wsSrv, testWSSessionConfig)

	// one byte short of the first chunk stays buffered
	if _, err := session.Write(make([]byte, wsFirstChunkBytes-1)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	wsSrv.noMessage(t, 100*time.Millisecond)

	if _, err := session.Write(make([]byte, 1)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := len(decodeAudio(t, wsSrv.nextMessage(t))); got != wsFirstChunkBytes {
		t.Errorf("first chunk = %d bytes, want %d", got, wsFirstChunkBytes)
	}

	// the rest of the segment goes out a second at a time
	if _, err := session.Write(make([]byte, wsSubsequentChunkBytes)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := len(decodeAudio(t, wsSrv.nextMessage(t))); got != wsSubsequentChunkBytes {
		t.Errorf("subsequent chunk = %d bytes, want %d", got, wsSubsequentChunkBytes)
	}

	// EndSegment flushes the tail and closes the segment
	if _, err := session.Write(make([]byte, 1000)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	session.EndSegment()

	tail := wsSrv.nextMessage(t)
	if tail.Type != "agent.speak" || len(decodeAudio(t, tail)) != 1000 {
		t.Errorf("tail = %+v, want the 1000 buffered bytes", tail)
	}
	end := wsSrv.nextMessage(t)
	if end.Type != "agent.speak_end" || end.EventID == "" {
		t.Errorf("end = %+v, want agent.speak_end with an event id", end)
	}
	if listen := wsSrv.nextMessage(t); listen.Type != "agent.start_listening" {
		t.Errorf("message = %q, want agent.start_listening", listen.Type)
	}

	// the next segment starts with the shorter first chunk again
	if _, err := session.Write(make([]byte, wsFirstChunkBytes)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := len(decodeAudio(t, wsSrv.nextMessage(t))); got != wsFirstChunkBytes {
		t.Errorf("chunk after the segment = %d bytes, want %d", got, wsFirstChunkBytes)
	}
}

func TestWSSessionFlushDropsBufferedAudio(t *testing.T) {
	wsSrv := newWSTestServer(t, false)
	session := newWSTestSession(t, wsSrv, testWSSessionConfig)

	if _, err := session.Write(make([]byte, 1000)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if session.RemainingDuration() <= 0 {
		t.Fatal("the written audio must be reported as playout")
	}

	session.Flush()

	if session.RemainingDuration() != 0 {
		t.Errorf("remaining playout = %v, want it cleared", session.RemainingDuration())
	}
	if interrupt := wsSrv.nextMessage(t); interrupt.Type != "agent.interrupt" || interrupt.EventID == "" {
		t.Errorf("message = %+v, want agent.interrupt with an event id", interrupt)
	}

	// the dropped audio is not sent by the end of the segment
	session.EndSegment()
	if next := wsSrv.nextMessage(t); next.Type != "agent.speak_end" {
		t.Errorf("message = %q, want agent.speak_end", next.Type)
	}
}

func TestWSSessionSetListening(t *testing.T) {
	wsSrv := newWSTestServer(t, false)
	session := newWSTestSession(t, wsSrv, testWSSessionConfig)

	session.SetListening(true)
	if message := wsSrv.nextMessage(t); message.Type != "agent.start_listening" {
		t.Errorf("message = %q, want agent.start_listening", message.Type)
	}
	session.SetListening(true)
	wsSrv.noMessage(t, 100*time.Millisecond)

	session.SetListening(false)
	if message := wsSrv.nextMessage(t); message.Type != "agent.stop_listening" {
		t.Errorf("message = %q, want agent.stop_listening", message.Type)
	}
	session.SetListening(false)
	wsSrv.noMessage(t, 100*time.Millisecond)
}

func TestWSSessionSpeakEndedResetsPlayout(t *testing.T) {
	wsSrv := newWSTestServer(t, false)
	session := newWSTestSession(t, wsSrv, testWSSessionConfig)

	// a flushed chunk leaves nothing buffered, so the avatar tells when it ends
	if _, err := session.Write(make([]byte, wsFirstChunkBytes)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	wsSrv.waitMessage(t, "agent.speak")
	wsSrv.send(t, map[string]any{"type": "agent.speak_ended"})
	waitFor(t, func() bool { return session.RemainingDuration() == 0 })

	// buffered audio means the segment is still running, so the playout stays
	if _, err := session.Write(make([]byte, wsFirstChunkBytes-1)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	wsSrv.send(t, map[string]any{"type": "agent.speak_ended"})
	time.Sleep(200 * time.Millisecond)
	if session.RemainingDuration() <= 0 {
		t.Error("the playout of a buffered segment must survive agent.speak_ended")
	}
}

func TestWSSessionCloseIsIdempotent(t *testing.T) {
	wsSrv := newWSTestServer(t, false)

	var calls int
	cfg := testWSSessionConfig
	cfg.closeFn = func(ctx context.Context) error {
		calls++
		return nil
	}
	session := newWSTestSession(t, wsSrv, cfg)

	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	session.AudioPublisher().Close()
	if calls != 1 {
		t.Errorf("close function calls = %d, want 1", calls)
	}
	if _, err := session.Write(make([]byte, 10)); err == nil {
		t.Error("a closed session must reject the audio")
	}
}
