package insightsservice

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

func testLogger() *logrus.Entry {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return logrus.NewEntry(l)
}

// fakeTranscriptionStream mimics one provider stream. Events are pushed by the
// test; Close closes the results channel exactly once, like a real provider.
type fakeTranscriptionStream struct {
	mu       sync.Mutex
	closed   bool
	closedCh chan struct{}
	results  chan *insights.TranscriptionEvent
	writes   chan media.PCM16Sample
	writeErr error
}

func newFakeStream() *fakeTranscriptionStream {
	return &fakeTranscriptionStream{
		closedCh: make(chan struct{}),
		results:  make(chan *insights.TranscriptionEvent, 32),
		writes:   make(chan media.PCM16Sample, 32),
	}
}

func (f *fakeTranscriptionStream) WriteSample(sample media.PCM16Sample) error {
	if f.writeErr != nil {
		return f.writeErr
	}

	select {
	case f.writes <- append(media.PCM16Sample(nil), sample...):
	default:
	}
	return nil
}

func (f *fakeTranscriptionStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return nil
	}
	f.closed = true
	close(f.closedCh)
	close(f.results)
	return nil
}

func (f *fakeTranscriptionStream) SetProperty(string, string) error { return nil }

func (f *fakeTranscriptionStream) Results() <-chan *insights.TranscriptionEvent { return f.results }

func (f *fakeTranscriptionStream) send(event *insights.TranscriptionEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.results <- event
}

func (f *fakeTranscriptionStream) isClosed() bool {
	select {
	case <-f.closedCh:
		return true
	default:
		return false
	}
}

// signalLog records observable side effects of a run in order.
type signalLog struct {
	mu     sync.Mutex
	events []string
}

func (s *signalLog) add(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *signalLog) count(event string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for _, e := range s.events {
		if e == event {
			n++
		}
	}
	return n
}

func (s *signalLog) waitFor(t *testing.T, event string, minCount int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.count(event) >= minCount {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q (have %v)", event, s.events)
}

func newTestRun(t *testing.T, dial func(context.Context) (insights.TranscriptionStream, error)) (*transcriptionRun, chan media.PCM16Sample, chan struct{}, *signalLog) {
	t.Helper()

	audio := make(chan media.PCM16Sample, 16)
	wake := make(chan struct{}, 1)
	log := testLogger()

	run := newTranscriptionRun(log, audio, wake, dial)
	run.retryBaseDelay = 5 * time.Millisecond
	run.readyTimeout = 2 * time.Second
	run.healthyAfter = time.Second
	run.toastDelay = 5 * time.Millisecond

	signals := &signalLog{}
	run.setUsage = func(active bool) {
		if active {
			signals.add("usage:true")
			return
		}
		signals.add("usage:false")
	}
	run.notifyReady = func() { signals.add("ready") }
	run.notifyStopped = func() { signals.add("stopped") }
	run.notifyFailed = func() { signals.add("failed") }

	return run, audio, wake, signals
}

func speechSamples(n int) media.PCM16Sample {
	out := make(media.PCM16Sample, n)
	for i := range out {
		out[i] = 1200
	}
	return out
}

// A provider disconnect must stop usage immediately, keep the task alive and
// resume usage on the next wake without a second "ready" notice.
func TestTranscriptionRunRecoversAfterDisconnect(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 4)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, audio, wake, signals := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	// First connection is started by the first audio sample.
	audio <- speechSamples(320)
	s1 := receiveStream(t, streams)
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	signals.waitFor(t, "usage:true", 1)
	signals.waitFor(t, "ready", 1)

	// The provider drops the stream.
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
	signals.waitFor(t, "usage:false", 1)

	// No automatic retry without audio or wake, and no stop notice.
	select {
	case s := <-streams:
		t.Fatalf("unexpected reconnect without audio or wake: %v", s)
	case <-time.After(50 * time.Millisecond):
	}
	if got := signals.count("stopped"); got != 0 {
		t.Fatalf("recoverable disconnect must not notify stopped, got %d", got)
	}

	// Unmute wake reconnects and usage resumes on the new ready stream.
	wake <- struct{}{}
	s2 := receiveStream(t, streams)
	s2.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	signals.waitFor(t, "usage:true", 2)

	time.Sleep(30 * time.Millisecond) // allow a (buggy) second ready notice to show up
	if got := signals.count("ready"); got != 1 {
		t.Fatalf("ready notice must be sent once per task, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}
}

// A stream that disconnects before the toast delay must not emit the ready
// notice; the next ready stream arms it again and sends it once.
func TestTranscriptionRunDisconnectBeforeToastCancelsNotice(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 4)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, _, wake, signals := newTestRun(t, dial)
	run.toastDelay = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	s1 := receiveStream(t, streams)
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
	signals.waitFor(t, "usage:false", 1)

	time.Sleep(300 * time.Millisecond)
	if got := signals.count("ready"); got != 0 {
		t.Fatalf("ready notice fired after the stream disconnected, got %d", got)
	}

	wake <- struct{}{}
	s2 := receiveStream(t, streams)
	s2.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	signals.waitFor(t, "ready", 1)

	time.Sleep(250 * time.Millisecond)
	if got := signals.count("ready"); got != 1 {
		t.Fatalf("ready notice must be sent once, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}
}

// Without a wake channel, the next audio sample must still recover the stream
// and audio buffered during the dial must be flushed to the new stream.
func TestTranscriptionRunRecoversOnAudioAndFlushesBuffer(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 4)
	releaseDial := make(chan struct{})

	first := true
	var mu sync.Mutex
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		mu.Lock()
		initial := first
		first = false
		mu.Unlock()

		if !initial {
			<-releaseDial
		}
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	audio := make(chan media.PCM16Sample, 16)
	run := newTranscriptionRun(testLogger(), audio, nil, dial)
	run.retryBaseDelay = 5 * time.Millisecond
	run.readyTimeout = 2 * time.Second
	run.healthyAfter = time.Second
	run.toastDelay = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	audio <- speechSamples(320)
	s1 := receiveStream(t, streams)
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStopped})
	waitClosed(t, s1)

	// Audio keeps flowing while the slow dial is in flight; it must be buffered.
	audio <- speechSamples(160)
	audio <- speechSamples(160)
	time.Sleep(20 * time.Millisecond)
	close(releaseDial)

	s2 := receiveStream(t, streams)
	s2.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})

	select {
	case wrote := <-s2.writes:
		if len(wrote) != 320 {
			t.Fatalf("expected buffered audio to be flushed, got %d samples", len(wrote))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("buffered audio was not written to the new stream")
	}

	cancel()
	<-done
}

// Dial errors are retried with a bounded budget and then reported once.
func TestTranscriptionRunRetryExhaustion(t *testing.T) {
	var attempts int
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		attempts++
		return nil, errors.New("dial refused")
	}

	run, audio, _, signals := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	audio <- speechSamples(320)

	select {
	case err := <-done:
		if !errors.Is(err, insights.ErrTranscriptionUnrecoverable) {
			t.Fatalf("expected unrecoverable error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after exhausting retries")
	}

	if attempts != transcriptionMaxConnectAttempts {
		t.Fatalf("expected %d dial attempts, got %d", transcriptionMaxConnectAttempts, attempts)
	}
	if got := signals.count("failed"); got != 1 {
		t.Fatalf("expected one failure notice, got %d", got)
	}
	if got := signals.count("stopped"); got != 0 {
		t.Fatalf("failure must not send the normal stop notice, got %d", got)
	}
	if got := signals.count("usage:true"); got != 0 {
		t.Fatalf("usage must never start without a ready provider, got %d", got)
	}
}

// A stream that never acknowledges the session must not leave the task enabled.
func TestTranscriptionRunReadinessTimeout(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 8)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, audio, _, signals := newTestRun(t, dial)
	run.readyTimeout = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	audio <- speechSamples(320)

	var seen []*fakeTranscriptionStream
	deadline := time.After(3 * time.Second)
	for len(seen) < transcriptionMaxConnectAttempts {
		select {
		case s := <-streams:
			seen = append(seen, s)
		case <-deadline:
			t.Fatalf("expected %d attempts, saw %d", transcriptionMaxConnectAttempts, len(seen))
		}
	}

	select {
	case err := <-done:
		if !errors.Is(err, insights.ErrTranscriptionUnrecoverable) {
			t.Fatalf("expected unrecoverable error, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not give up after readiness timeouts")
	}

	for _, s := range seen {
		waitClosed(t, s)
	}
	if got := signals.count("usage:true"); got != 0 {
		t.Fatalf("unacknowledged sessions must not start usage, got %d", got)
	}
}

// A pre-ready provider error is terminal for the attempt and closes the socket.
func TestTranscriptionRunRetriesOnPreReadyError(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 4)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, audio, _, signals := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	audio <- speechSamples(320)
	s1 := receiveStream(t, streams)
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeError, Error: "invalid api key"})
	waitClosed(t, s1)

	s2 := receiveStream(t, streams)
	s2.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	signals.waitFor(t, "usage:true", 1)

	cancel()
	<-done
}

// Cancelling while a dial is in flight must return promptly and close the late
// stream instead of leaking it.
func TestTranscriptionRunCancellationDuringSlowDial(t *testing.T) {
	release := make(chan struct{})
	late := newFakeStream()
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		<-release
		return late, nil
	}

	run, audio, _, _ := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	audio <- speechSamples(320)
	time.Sleep(10 * time.Millisecond)

	cancel()
	close(release)

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}

	waitClosed(t, late)
}

// An approved task must connect right away, even when the participant never
// sends audio (muted) and never unmutes.
func TestTranscriptionRunConnectsWhileIdle(t *testing.T) {
	dialed := make(chan struct{}, 1)
	stream := newFakeStream()
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		dialed <- struct{}{}
		return stream, nil
	}

	run, _, _, _ := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	select {
	case <-dialed:
	case <-time.After(2 * time.Second):
		t.Fatal("initial dial must start without audio or wake")
	}

	// The idle connection is only torn down when the run stops.
	select {
	case <-stream.closedCh:
		t.Fatal("idle initial connection must stay open")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}
}

// Closing the audio channel while a dial is in flight must end the run and the
// late stream must be released by whoever still owns it.
func TestTranscriptionRunAudioCloseDuringDialReleasesStream(t *testing.T) {
	release := make(chan struct{})
	late := newFakeStream()
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		<-release
		return late, nil
	}

	run, audio, _, signals := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	time.Sleep(10 * time.Millisecond)
	close(audio)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected a clean stop, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not stop after the audio channel closed")
	}

	close(release)
	waitClosed(t, late)
	if got := signals.count("stopped"); got != 1 {
		t.Fatalf("expected one stop notice, got %d", got)
	}
}

// A cancellation that races with a completed dial must never leak the stream,
// regardless of which side wins the handoff.
func TestTranscriptionRunCancellationRaceReleasesStream(t *testing.T) {
	for i := 0; i < 25; i++ {
		stream := newFakeStream()
		dialReturned := make(chan struct{})
		dial := func(context.Context) (insights.TranscriptionStream, error) {
			close(dialReturned)
			return stream, nil
		}

		run, _, _, _ := newTestRun(t, dial)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- run.run(ctx) }()

		<-dialReturned
		cancel()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("run did not stop after cancellation")
		}
		waitClosed(t, stream)
	}
}

// No task PCM may reach a provider stream before it acknowledged the session;
// buffered audio is flushed in order once it does.
func TestTranscriptionRunWritesOnlyAfterReady(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 2)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, audio, _, signals := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	s1 := receiveStream(t, streams)
	audio <- speechSamples(160)
	audio <- speechSamples(80)

	select {
	case wrote := <-s1.writes:
		t.Fatalf("audio was written before the session was ready: %d samples", len(wrote))
	case <-time.After(50 * time.Millisecond):
	}

	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeSessionStarted})
	signals.waitFor(t, "usage:true", 1)

	select {
	case wrote := <-s1.writes:
		if len(wrote) != 240 {
			t.Fatalf("expected the buffered audio in order (240 samples), got %d", len(wrote))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("buffered audio was not flushed after the session was ready")
	}

	cancel()
	<-done
}

// A rejected session must never receive the audio buffered while unready.
func TestTranscriptionRunRejectedSessionGetsNoAudio(t *testing.T) {
	streams := make(chan *fakeTranscriptionStream, 2)
	dial := func(context.Context) (insights.TranscriptionStream, error) {
		s := newFakeStream()
		streams <- s
		return s, nil
	}

	run, audio, _, _ := newTestRun(t, dial)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run.run(ctx) }()

	s1 := receiveStream(t, streams)
	audio <- speechSamples(160)
	s1.send(&insights.TranscriptionEvent{Type: insights.EventTypeError, Error: "invalid model"})
	waitClosed(t, s1)

	select {
	case wrote := <-s1.writes:
		t.Fatalf("rejected session received %d samples", len(wrote))
	default:
	}

	cancel()
	<-done
}

func receiveStream(t *testing.T, streams <-chan *fakeTranscriptionStream) *fakeTranscriptionStream {
	t.Helper()

	select {
	case s := <-streams:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a new provider stream")
		return nil
	}
}

func waitClosed(t *testing.T, s *fakeTranscriptionStream) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.isClosed() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("stream was not closed")
}
