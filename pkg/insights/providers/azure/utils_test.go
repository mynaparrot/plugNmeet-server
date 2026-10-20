package azure

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

func testLog() *logrus.Entry {
	l := logrus.New()
	l.SetLevel(logrus.PanicLevel)
	return logrus.NewEntry(l)
}

// Close must release resources exactly once and never block, even when called
// repeatedly or concurrently with SDK callbacks pushing events.
func TestAzureTranscribeStreamCloseIsIdempotent(t *testing.T) {
	// No SDK objects here: this only exercises the ownership/close guards.
	stream := newAzureTranscribeStream(context.Background(), nil, nil, nil, nil, testLog())
	stream.startFinished(false)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stream.send(&insights.TranscriptionEvent{Type: insights.EventTypePartialResult})
		}()
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 4; i++ {
			if err := stream.Close(); err != nil {
				t.Errorf("Close returned error: %v", err)
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
	wg.Wait()

	// Sends after close are dropped instead of panicking.
	stream.send(&insights.TranscriptionEvent{Type: insights.EventTypeFinalResult})

	closedEvents := 0
	for range stream.Results() {
		closedEvents++
	}
	if closedEvents > 64 {
		t.Fatalf("unexpected number of buffered events: %d", closedEvents)
	}
}

// Write and SetProperty must fail closed instead of touching released handles.
func TestAzureTranscribeStreamRejectsUseAfterClose(t *testing.T) {
	stream := newAzureTranscribeStream(context.Background(), nil, nil, nil, nil, testLog())
	stream.startFinished(false)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if err := stream.WriteSample(media.PCM16Sample{1, 2, 3}); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF from WriteSample after close, got %v", err)
	}
	if err := stream.SetProperty("k", "v"); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF from SetProperty after close, got %v", err)
	}
}

// Cancelling the parent context must release the stream without an explicit
// Close call, and the results channel must end.
func TestAzureTranscribeStreamStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newAzureTranscribeStream(ctx, nil, nil, nil, nil, testLog())
	stream.startFinished(false)

	cancel()

	select {
	case _, ok := <-stream.Results():
		if ok {
			// Drain any buffered event, then expect the channel to be closed.
			for range stream.Results() {
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("results channel was not closed after context cancellation")
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

// A cancellation (or an early terminal SDK callback) that arrives while
// CreateTranscription is still starting the recognizer must not release the
// SDK objects; teardown waits for the initialization gate to open.
func TestAzureTranscribeStreamTeardownWaitsForInitialization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newAzureTranscribeStream(ctx, nil, nil, nil, nil, testLog())

	cancel()

	closed := make(chan struct{})
	go func() {
		_ = stream.Close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("teardown must not run before initialization finished")
	case <-time.After(100 * time.Millisecond):
	}

	stream.startFinished(false)

	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after initialization finished")
	}

	if _, ok := <-stream.Results(); ok {
		t.Fatal("results channel must be closed after teardown")
	}
}

// Cancellation must report the terminal session event exactly once, after the
// provider error, so the task stops usage before the blocking teardown.
func TestAzureTranscribeStreamTerminalEventIsSentOnce(t *testing.T) {
	stream := newAzureTranscribeStream(context.Background(), nil, nil, nil, nil, testLog())

	stream.send(&insights.TranscriptionEvent{Type: insights.EventTypeError, Error: "canceled"})
	stream.sendTerminal()
	stream.sendTerminal()

	if event := <-stream.Results(); event.Type != insights.EventTypeError {
		t.Fatalf("expected the provider error first, got %s", event.Type)
	}
	if event := <-stream.Results(); event.Type != insights.EventTypeSessionStopped {
		t.Fatalf("expected the terminal event, got %s", event.Type)
	}

	stream.startFinished(false)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	for event := range stream.Results() {
		if event.Type == insights.EventTypeSessionStopped {
			t.Fatal("the terminal event must be sent only once")
		}
	}
}

// The terminal event must survive a full results buffer instead of being
// silently dropped while the SDK teardown may be stuck.
func TestAzureTranscribeStreamTerminalEventSurvivesFullBuffer(t *testing.T) {
	stream := newAzureTranscribeStream(context.Background(), nil, nil, nil, nil, testLog())
	stream.startFinished(false)

	for i := 0; i < cap(stream.results); i++ {
		stream.send(&insights.TranscriptionEvent{Type: insights.EventTypePartialResult})
	}
	stream.sendTerminal()

	var last *insights.TranscriptionEvent
	for i := 0; i < cap(stream.results); i++ {
		last = <-stream.Results()
	}
	if last.Type != insights.EventTypeSessionStopped {
		t.Fatalf("terminal event was dropped from a full buffer, last=%s", last.Type)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestHasText(t *testing.T) {
	if hasText("   ") {
		t.Fatal("empty/NoMatch text must be filtered")
	}
	if !hasText("hello") {
		t.Fatal("real text must be kept")
	}
}
