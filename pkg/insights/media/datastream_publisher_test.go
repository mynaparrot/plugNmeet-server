package media

import (
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

func int16SamplesToBytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}

func bytesToInt16Samples(data []byte) []int16 {
	out := make([]int16, len(data)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	return out
}

func TestResamplePCM16Mono24kTo16kByteLength(t *testing.T) {
	const numSamples = 2400 // 50 ms at 24 kHz
	in := make([]byte, numSamples*2)

	out := ResamplePCM16Mono24kTo16k(in)

	if len(out) != 3200 {
		t.Fatalf("expected 3200 bytes (1600 samples @ 16kHz), got %d", len(out))
	}
}

func TestResamplePCM16Mono24kTo16kTailSamples(t *testing.T) {
	cases := []struct {
		name          string
		inputSamples  int
		outputSamples int
	}{
		{name: "no samples", inputSamples: 0, outputSamples: 0},
		{name: "single sample", inputSamples: 1, outputSamples: 1},
		{name: "two samples", inputSamples: 2, outputSamples: 1},
		{name: "exactly one group", inputSamples: 3, outputSamples: 2},
		{name: "group plus one", inputSamples: 4, outputSamples: 3},
		{name: "group plus two", inputSamples: 5, outputSamples: 3},
		{name: "two groups plus one", inputSamples: 7, outputSamples: 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ResamplePCM16Mono24kTo16k(make([]byte, tc.inputSamples*2))
			if len(out) != tc.outputSamples*2 {
				t.Fatalf("expected %d samples (%d bytes), got %d bytes", tc.outputSamples, tc.outputSamples*2, len(out))
			}
		})
	}
}

func TestResamplePCM16Mono24kTo16kIgnoresTrailingByte(t *testing.T) {
	// 5 bytes is two complete 16-bit samples plus a stray byte.
	out := ResamplePCM16Mono24kTo16k([]byte{0x00, 0x00, 0x10, 0x00, 0xFF})
	if len(out) != 2 {
		t.Fatalf("expected 1 output sample (2 bytes), got %d bytes", len(out))
	}
	if got := bytesToInt16Samples(out)[0]; got != 0 {
		t.Fatalf("expected first sample to be kept, got %d", got)
	}
}

func TestResamplePCM16Mono24kTo16kAveraging(t *testing.T) {
	in := int16SamplesToBytes([]int16{100, 200, 300, 400, 500, 600, 700})

	out := bytesToInt16Samples(ResamplePCM16Mono24kTo16k(in))
	expected := []int16{100, 250, 400, 550, 700}

	if len(out) != len(expected) {
		t.Fatalf("expected %d samples, got %d", len(expected), len(out))
	}
	for i := range expected {
		if out[i] != expected[i] {
			t.Fatalf("sample %d: expected %d, got %d", i, expected[i], out[i])
		}
	}
}

func TestResamplePCM16Mono24kTo16kSignalFidelity(t *testing.T) {
	const (
		numSamples = 2400 // 100 ms at 24 kHz
		frequency  = 1000.0
		amplitude  = 12000.0
	)

	in := make([]int16, numSamples)
	for i := range in {
		in[i] = int16(amplitude * math.Sin(2*math.Pi*frequency*float64(i)/DefaultOutputSampleRate24k))
	}

	out := bytesToInt16Samples(ResamplePCM16Mono24kTo16k(int16SamplesToBytes(in)))
	if len(out) != 1600 {
		t.Fatalf("expected 1600 samples, got %d", len(out))
	}

	rms := func(samples []int16) float64 {
		var sum float64
		for _, s := range samples {
			sum += float64(s) * float64(s)
		}
		return math.Sqrt(sum / float64(len(samples)))
	}
	inRMS, outRMS := rms(in), rms(out)
	if diff := math.Abs(outRMS-inRMS) / inRMS; diff > 0.02 {
		t.Fatalf("RMS changed too much: in %f out %f (%f%%)", inRMS, outRMS, diff*100)
	}

	var maxDiff float64
	for i, got := range out {
		want := amplitude * math.Sin(2*math.Pi*frequency*float64(i)/DefaultSampleRate16k)
		if diff := math.Abs(float64(got) - want); diff > maxDiff {
			maxDiff = diff
		}
	}
	if maxDiff > amplitude*0.02 {
		t.Fatalf("output deviates from a 16kHz sine by %f (max allowed %f)", maxDiff, amplitude*0.02)
	}
}

// fakeByteStreamWriter records what the publisher writes to a stream.
type fakeByteStreamWriter struct {
	mu         sync.Mutex
	writes     [][]byte
	onDones    []*func()
	closed     bool
	closeCount int
}

func (f *fakeByteStreamWriter) Write(data []byte, onDone *func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), data...))
	f.onDones = append(f.onDones, onDone)
}

func (f *fakeByteStreamWriter) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closeCount++
}

func (f *fakeByteStreamWriter) snapshot() (writes [][]byte, closed bool, closeCount int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writes = make([][]byte, len(f.writes))
	copy(writes, f.writes)
	return writes, f.closed, f.closeCount
}

// fakeStreamFactory stands in for room.LocalParticipant.StreamBytes.
type fakeStreamFactory struct {
	mu      sync.Mutex
	options []lksdk.StreamBytesOptions
	writers []*fakeByteStreamWriter
}

func (f *fakeStreamFactory) open(options lksdk.StreamBytesOptions) byteStreamWriter {
	f.mu.Lock()
	defer f.mu.Unlock()
	writer := &fakeByteStreamWriter{}
	f.options = append(f.options, options)
	f.writers = append(f.writers, writer)
	return writer
}

func (f *fakeStreamFactory) openedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writers)
}

func (f *fakeStreamFactory) writerAt(t *testing.T, index int) *fakeByteStreamWriter {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.writers) {
		t.Fatalf("stream %d was never opened", index)
	}
	return f.writers[index]
}

func (f *fakeStreamFactory) optionsAt(t *testing.T, index int) lksdk.StreamBytesOptions {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.options) {
		t.Fatalf("stream %d was never opened", index)
	}
	return f.options[index]
}

// fakeRPC stands in for room.LocalParticipant.PerformRpc.
type fakeRPC struct {
	calls chan lksdk.PerformRpcParams
}

func newFakeRPC() *fakeRPC {
	return &fakeRPC{calls: make(chan lksdk.PerformRpcParams, 4)}
}

func (f *fakeRPC) call(params lksdk.PerformRpcParams) (*string, error) {
	f.calls <- params
	return nil, nil
}

func newTestDataStreamPublisher(t *testing.T, targetSampleRate int, streams *fakeStreamFactory, rpc performRPCFunc) *DataStreamAudioPublisher {
	t.Helper()
	return &DataStreamAudioPublisher{
		destinationIdentity: "user-1",
		targetSampleRate:    targetSampleRate,
		openStream:          streams.open,
		performRPC:          rpc,
	}
}

func TestNewDataStreamAudioPublisherValidation(t *testing.T) {
	room := lksdk.NewRoom(&lksdk.RoomCallback{})

	if _, err := NewDataStreamAudioPublisher(nil, "user-1", DefaultSampleRate16k); err == nil {
		t.Fatal("expected an error for a nil room")
	}
	if _, err := NewDataStreamAudioPublisher(&lksdk.Room{}, "user-1", DefaultSampleRate16k); err == nil {
		t.Fatal("expected an error for a room without a local participant")
	}
	if _, err := NewDataStreamAudioPublisher(room, "", DefaultSampleRate16k); err == nil {
		t.Fatal("expected an error for an empty destination identity")
	}
	if _, err := NewDataStreamAudioPublisher(room, "user-1", 8000); err == nil {
		t.Fatal("expected an error for an unsupported sample rate")
	}

	p, err := NewDataStreamAudioPublisher(room, "user-1", DefaultSampleRate16k)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.openStream == nil || p.performRPC == nil || p.waitReady == nil {
		t.Fatal("expected room-backed callbacks to be wired")
	}
	if p.readyPollInterval != DefaultReadyPollInterval || p.readyTimeout != DefaultReadyTimeout {
		t.Fatalf("unexpected readiness timings: %s/%s", p.readyPollInterval, p.readyTimeout)
	}
	// Nobody has joined the empty room yet, so the destination is not ready.
	if p.waitReady() {
		t.Fatal("expected the destination to be missing in an empty room")
	}
	if p.targetSampleRate != DefaultSampleRate16k || p.destinationIdentity != "user-1" {
		t.Fatalf("unexpected publisher fields: %+v", p)
	}
}

func TestDataStreamAudioPublisherLazyOpenAndReuse(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, newFakeRPC().call)

	if _, err := p.Write(nil); err != nil {
		t.Fatalf("unexpected error for empty write: %v", err)
	}
	if _, err := p.Write([]byte{0x01}); err != nil {
		t.Fatalf("unexpected error for short write: %v", err)
	}
	if streams.openedCount() != 0 {
		t.Fatal("no stream should be opened for a payload shorter than one sample")
	}

	pcm := make([]byte, 4800) // 2400 samples @ 24kHz
	if n, err := p.Write(pcm); err != nil || n != len(pcm) {
		t.Fatalf("unexpected write result: n=%d err=%v", n, err)
	}
	if streams.openedCount() != 1 {
		t.Fatalf("expected exactly one stream, got %d", streams.openedCount())
	}

	options := streams.optionsAt(t, 0)
	if options.Topic != AudioStreamTopic {
		t.Fatalf("unexpected topic: %q", options.Topic)
	}
	if len(options.DestinationIdentities) != 1 || options.DestinationIdentities[0] != "user-1" {
		t.Fatalf("unexpected destinations: %v", options.DestinationIdentities)
	}
	if options.Attributes["sample_rate"] != "16000" || options.Attributes["num_channels"] != "1" {
		t.Fatalf("unexpected attributes: %v", options.Attributes)
	}

	writes, closed, _ := streams.writerAt(t, 0).snapshot()
	if len(writes) != 1 {
		t.Fatalf("expected 1 write, got %d", len(writes))
	}
	if len(writes[0]) != 3200 {
		t.Fatalf("expected the 16kHz payload (3200 bytes), got %d", len(writes[0]))
	}
	if closed {
		t.Fatal("stream must stay open between writes of one segment")
	}

	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error on second write: %v", err)
	}
	if streams.openedCount() != 1 {
		t.Fatalf("a second write must reuse the stream, opened %d", streams.openedCount())
	}
	writes, _, _ = streams.writerAt(t, 0).snapshot()
	if len(writes) != 2 {
		t.Fatalf("expected 2 writes on the same stream, got %d", len(writes))
	}
}

// newTestPublisherWithReadiness builds a publisher whose readiness comes from
// waitReady, with a short poll interval so tests stay fast.
func newTestPublisherWithReadiness(t *testing.T, streams *fakeStreamFactory, waitReady func() bool, timeout time.Duration) *DataStreamAudioPublisher {
	t.Helper()
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, newFakeRPC().call)
	p.waitReady = waitReady
	p.readyPollInterval = time.Millisecond
	p.readyTimeout = timeout
	return p
}

// The avatar bot joins after its session API call returned, so the first write
// must wait until the destination participant is in the room instead of opening
// a stream nobody receives.
func TestDataStreamAudioPublisherWaitsForDestination(t *testing.T) {
	streams := &fakeStreamFactory{}
	var calls int
	p := newTestPublisherWithReadiness(t, streams, func() bool {
		calls++
		return calls >= 3
	}, 5*time.Second)

	if _, err := p.Write(make([]byte, 480)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls < 3 {
		t.Fatalf("waitReady called %d times, want the poll loop to retry", calls)
	}
	if !p.ready {
		t.Fatal("expected the publisher to remember the destination was seen")
	}
	if streams.openedCount() != 1 {
		t.Fatalf("expected exactly one stream, got %d", streams.openedCount())
	}
}

// Once the destination has been seen, later segments must not wait again.
func TestDataStreamAudioPublisherSkipsWaitAfterDestinationReady(t *testing.T) {
	streams := &fakeStreamFactory{}
	var calls int
	p := newTestPublisherWithReadiness(t, streams, func() bool {
		calls++
		return true
	}, 5*time.Second)

	if _, err := p.Write(make([]byte, 480)); err != nil {
		t.Fatalf("unexpected error on first write: %v", err)
	}
	p.EndSegment()
	if _, err := p.Write(make([]byte, 480)); err != nil {
		t.Fatalf("unexpected error on second write: %v", err)
	}

	if calls != 1 {
		t.Fatalf("waitReady called %d times, want only the first stream to wait", calls)
	}
	if streams.openedCount() != 2 {
		t.Fatalf("expected two streams, got %d", streams.openedCount())
	}
}

// A destination that never joins must not block the caller forever.
func TestDataStreamAudioPublisherDestinationTimeout(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestPublisherWithReadiness(t, streams, func() bool { return false }, 30*time.Millisecond)

	n, err := p.Write(make([]byte, 480))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if n != 0 {
		t.Errorf("Write returned %d bytes, want 0", n)
	}
	if !strings.Contains(err.Error(), "did not join") {
		t.Errorf("error = %q, want it to mention the missing destination", err)
	}
	if streams.openedCount() != 0 {
		t.Fatalf("a timed out write must not open a stream, got %d", streams.openedCount())
	}
	if p.ready {
		t.Fatal("a timed out write must not mark the destination ready")
	}
}

// Close must interrupt a write that is still waiting for the destination.
func TestDataStreamAudioPublisherWaitStopsWhenClosed(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestPublisherWithReadiness(t, streams, func() bool { return false }, 10*time.Second)

	done := make(chan error, 1)
	go func() {
		_, err := p.Write(make([]byte, 480))
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after Close")
		}
		if !strings.Contains(err.Error(), "closed") {
			t.Errorf("error = %q, want it to report the closed publisher", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Write did not return after Close")
	}
	if streams.openedCount() != 0 {
		t.Fatalf("a closed publisher must not open streams, got %d", streams.openedCount())
	}
}

func TestDataStreamAudioPublisherKeeps24kPayload(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestDataStreamPublisher(t, DefaultOutputSampleRate24k, streams, newFakeRPC().call)

	pcm := make([]byte, 4800)
	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	options := streams.optionsAt(t, 0)
	if options.Attributes["sample_rate"] != "24000" {
		t.Fatalf("unexpected sample_rate attribute: %v", options.Attributes)
	}

	writes, _, _ := streams.writerAt(t, 0).snapshot()
	if len(writes) != 1 || len(writes[0]) != len(pcm) {
		t.Fatalf("expected the untouched 24kHz payload, got %d writes", len(writes))
	}
}

// The playout estimate must follow the rate the audio is streamed at, so an
// avatar target never reports more queued audio than the listener will hear.
func TestDataStreamAudioPublisherPlayoutFollowsWireRate(t *testing.T) {
	pcm := make([]byte, 4800) // 2400 samples @ 24 kHz mono = 100 ms of audio

	for _, rate := range []int{DefaultSampleRate16k, DefaultOutputSampleRate24k} {
		streams := &fakeStreamFactory{}
		p := newTestDataStreamPublisher(t, rate, streams, newFakeRPC().call)

		if _, err := p.Write(pcm); err != nil {
			t.Fatalf("unexpected error at %d Hz: %v", rate, err)
		}

		rem := p.RemainingDuration()
		if rem < 80*time.Millisecond || rem > 110*time.Millisecond {
			t.Fatalf("remaining playout at %d Hz = %s, want about 100ms", rate, rem)
		}
	}
}

func TestDataStreamAudioPublisherEndSegment(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, newFakeRPC().call)
	pcm := make([]byte, 480)

	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.EndSegment()

	if _, closed, closeCount := streams.writerAt(t, 0).snapshot(); !closed || closeCount != 1 {
		t.Fatalf("expected the first stream to be closed once, closed=%v count=%d", closed, closeCount)
	}
	if p.writer != nil {
		t.Fatal("expected the writer to be cleared after EndSegment")
	}

	p.EndSegment() // no active writer, must not panic

	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if streams.openedCount() != 2 {
		t.Fatalf("expected a new stream after EndSegment, got %d", streams.openedCount())
	}
	if _, closed, _ := streams.writerAt(t, 1).snapshot(); closed {
		t.Fatal("the new stream must stay open")
	}
}

func TestDataStreamAudioPublisherPlaybackFinishedResetsPlayout(t *testing.T) {
	streams := &fakeStreamFactory{}
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, newFakeRPC().call)
	pcm := make([]byte, 4800) // 150 ms at 16 kHz mono
	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The closed segment played out: the estimate must drop to zero.
	p.EndSegment()
	p.onPlaybackFinished("user-1")
	if rem := p.RemainingDuration(); rem != 0 {
		t.Fatalf("the finished playback must clear the playout, got %s", rem)
	}

	// An open segment still holds audio behind it, so the estimate stands.
	if _, err := p.Write(pcm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p.onPlaybackFinished("")
	if rem := p.RemainingDuration(); rem == 0 {
		t.Fatal("audio written after the report is still queued")
	}

	// Another caller finishing its own playback says nothing about this one.
	p.EndSegment()
	p.onPlaybackFinished("user-2")
	if rem := p.RemainingDuration(); rem == 0 {
		t.Fatal("a foreign caller must not touch the playout")
	}
}

func TestDataStreamAudioPublisherFlush(t *testing.T) {
	streams := &fakeStreamFactory{}
	rpc := newFakeRPC()
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, rpc.call)

	if _, err := p.Write(make([]byte, 480)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.Flush()

	if _, closed, closeCount := streams.writerAt(t, 0).snapshot(); !closed || closeCount != 1 {
		t.Fatalf("expected Flush to close the stream, closed=%v count=%d", closed, closeCount)
	}

	select {
	case params := <-rpc.calls:
		if params.DestinationIdentity != "user-1" {
			t.Fatalf("unexpected rpc destination: %q", params.DestinationIdentity)
		}
		if params.Method != AudioStreamClearMethod {
			t.Fatalf("unexpected rpc method: %q", params.Method)
		}
		if params.ResponseTimeout == nil || *params.ResponseTimeout != clearBufferRPCTimeout {
			t.Fatalf("unexpected rpc timeout: %v", params.ResponseTimeout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected Flush to fire the clear-buffer RPC")
	}

	// A barge-in arriving before any audio is written still asks the client to
	// clear, but must not open a stream.
	p.Flush()
	select {
	case <-rpc.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("expected Flush to fire the clear-buffer RPC without an open stream")
	}
	if streams.openedCount() != 1 {
		t.Fatalf("Flush must not open streams, got %d", streams.openedCount())
	}
}

func TestDataStreamAudioPublisherClose(t *testing.T) {
	streams := &fakeStreamFactory{}
	rpc := newFakeRPC()
	p := newTestDataStreamPublisher(t, DefaultSampleRate16k, streams, rpc.call)

	if _, err := p.Write(make([]byte, 480)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.Close()

	if _, closed, closeCount := streams.writerAt(t, 0).snapshot(); !closed || closeCount != 1 {
		t.Fatalf("expected Close to close the stream, closed=%v count=%d", closed, closeCount)
	}
	if !p.closed {
		t.Fatal("expected the publisher to be marked closed")
	}

	if n, err := p.Write(make([]byte, 480)); err == nil {
		t.Fatalf("expected an error after Close, got n=%d", n)
	}
	if streams.openedCount() != 1 {
		t.Fatalf("a closed publisher must not open streams, got %d", streams.openedCount())
	}

	p.Close() // idempotent
	p.EndSegment()
	if _, _, closeCount := streams.writerAt(t, 0).snapshot(); closeCount != 1 {
		t.Fatalf("expected the stream to be closed exactly once, got %d", closeCount)
	}
}
