package media

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"
)

const (
	// AudioStreamTopic is the LiveKit data stream topic carrying agent audio.
	AudioStreamTopic = "lk.audio_stream"
	// AudioStreamClearMethod is the RPC a client handles to drop the audio it has
	// buffered but not played yet. It is how barge-in is delivered.
	AudioStreamClearMethod = "lk.clear_buffer"
	// AudioStreamPlaybackFinishedMethod is the RPC a livekit-agents
	// DataStreamAudioReceiver issues (notify_playback_finished) on the agent
	// participant at the end of an utterance. b-server registers a no-op handler
	// so the receiver's call succeeds instead of failing with UNSUPPORTED_METHOD,
	// which it would otherwise retry three times per segment.
	AudioStreamPlaybackFinishedMethod = "lk.playback_finished"
	// clearBufferRPCTimeout bounds the clear RPC so a silent client cannot leave
	// the call hanging.
	clearBufferRPCTimeout = 1500 * time.Millisecond

	// DefaultReadyPollInterval is how often the publisher re-checks whether the
	// destination participant has joined the room.
	DefaultReadyPollInterval = 50 * time.Millisecond
	// DefaultReadyTimeout bounds how long the first stream open waits for the
	// destination participant to join the room. It matches livekit-agents'
	// wait_for_join(timeout=30.0).
	DefaultReadyTimeout = 30 * time.Second
)

// byteStreamWriter is the subset of *lksdk.ByteStreamWriter the publisher needs,
// which keeps the publisher unit-testable without a live WebRTC connection.
type byteStreamWriter interface {
	Write(data []byte, onDone *func())
	Close()
}

// openByteStreamFunc opens a new byte stream. In production it wraps
// room.LocalParticipant.StreamBytes.
type openByteStreamFunc func(options lksdk.StreamBytesOptions) byteStreamWriter

// performRPCFunc calls a method on a remote participant. In production it wraps
// room.LocalParticipant.PerformRpc.
type performRPCFunc func(params lksdk.PerformRpcParams) (*string, error)

// DataStreamAudioPublisher sends agent audio to a single participant over a
// LiveKit byte stream instead of a published track. Each segment (one turn of
// speech) is its own stream, so a client can tell where speech starts and ends,
// and Flush both closes the current stream and asks the client to drop what it
// has buffered.
type DataStreamAudioPublisher struct {
	destinationIdentity string
	targetSampleRate    int
	numChannels         int

	openStream openByteStreamFunc
	performRPC performRPCFunc

	// waitReady reports whether the destination participant is in the room, so
	// the stream opens only once there is someone to receive it. An avatar bot
	// joins some time after its session API call returned, so the first stream
	// open waits for it; nil disables the wait.
	waitReady func() bool
	// readyPollInterval and readyTimeout bound that wait. Zero values fall back
	// to DefaultReadyPollInterval and DefaultReadyTimeout.
	readyPollInterval time.Duration
	readyTimeout      time.Duration

	// readyMu serializes the wait for the destination, so one goroutine polls
	// while the others block until it has been seen. ready is guarded by it.
	readyMu sync.Mutex
	ready   bool

	mu     sync.Mutex
	writer byteStreamWriter
	closed bool
	// playoutUntil is when the audio handed to the destination finishes playing.
	playoutUntil time.Time
}

// NewDataStreamAudioPublisher creates a publisher that streams audio to
// destinationIdentity. targetSampleRate is the rate of the stream landing on the
// wire: DefaultSampleRate16k or DefaultOutputSampleRate24k.
func NewDataStreamAudioPublisher(room *lksdk.Room, destinationIdentity string, targetSampleRate int) (*DataStreamAudioPublisher, error) {
	if room == nil || room.LocalParticipant == nil {
		return nil, errors.New("datastream audio publisher: room is not connected")
	}
	if destinationIdentity == "" {
		return nil, errors.New("datastream audio publisher: destination identity is required")
	}
	if targetSampleRate != DefaultSampleRate16k && targetSampleRate != DefaultOutputSampleRate24k {
		return nil, fmt.Errorf("datastream audio publisher: unsupported sample rate %d", targetSampleRate)
	}

	localParticipant := room.LocalParticipant

	publisher := &DataStreamAudioPublisher{
		destinationIdentity: destinationIdentity,
		targetSampleRate:    targetSampleRate,
		numChannels:         1,
		openStream: func(options lksdk.StreamBytesOptions) byteStreamWriter {
			return localParticipant.StreamBytes(options)
		},
		performRPC: func(params lksdk.PerformRpcParams) (*string, error) {
			return localParticipant.PerformRpc(params)
		},
		// Only the presence of the destination participant is required. Avatar
		// workers built on livekit-agents' AvatarRunner use _lazy_publish = True
		// by default, so their Camera/Mic tracks are published only after the
		// first lk.audio_stream frames arrive; waiting for a video track here
		// would deadlock against that.
		waitReady: func() bool {
			for _, participant := range room.GetRemoteParticipants() {
				if participant.Identity() == destinationIdentity {
					return true
				}
			}
			return false
		},
		readyPollInterval: DefaultReadyPollInterval,
		readyTimeout:      DefaultReadyTimeout,
	}

	// The avatar worker's DataStreamAudioReceiver calls back on this method after
	// it finishes playing a segment, which is the only exact end of the playout.
	// Without a handler the call fails with UNSUPPORTED_METHOD and is retried
	// three times per utterance. A duplicate registration (this room already
	// serving another publisher) is harmless and not fatal for the publisher.
	_ = room.RegisterRpcMethod(AudioStreamPlaybackFinishedMethod, func(data lksdk.RpcInvocationData) (string, error) {
		publisher.onPlaybackFinished(data.CallerIdentity)
		return "", nil
	})

	return publisher, nil
}

// onPlaybackFinished drops the playout horizon once the destination reports that
// the segment it received has been heard.
func (p *DataStreamAudioPublisher) onPlaybackFinished(callerIdentity string) {
	if callerIdentity != "" && callerIdentity != p.destinationIdentity {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	// A nil writer means EndSegment already closed the segment, so nothing new is
	// queued behind the audio that just finished.
	if p.writer == nil {
		p.playoutUntil = time.Time{}
	}
}

// waitForDestination blocks until the destination participant has joined the
// room, which is what makes the first stream of an avatar session land: the bot
// joins after its session API call returned. The wait deliberately does not
// require any published track, because AvatarRunner lazily publishes its tracks
// only after the first audio frames arrive. Once the destination has been seen
// the wait is skipped, and a closed publisher stops waiting immediately.
func (p *DataStreamAudioPublisher) waitForDestination() error {
	if p.waitReady == nil {
		return nil
	}

	p.readyMu.Lock()
	defer p.readyMu.Unlock()

	if p.ready {
		return nil
	}
	if p.waitReady() {
		p.ready = true
		return nil
	}

	interval, timeout := p.readyWaitTimings()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		select {
		case <-ticker.C:
			if p.isClosed() {
				return errors.New("datastream audio publisher is closed")
			}
			if p.waitReady() {
				p.ready = true
				return nil
			}
		case <-deadline.C:
			return fmt.Errorf("datastream audio publisher: destination participant %q did not join the room within %s", p.destinationIdentity, timeout)
		}
	}
}

// readyWaitTimings returns the effective poll interval and timeout, so a zero
// valued publisher still gets the package defaults.
func (p *DataStreamAudioPublisher) readyWaitTimings() (interval, timeout time.Duration) {
	interval, timeout = p.readyPollInterval, p.readyTimeout
	if interval <= 0 {
		interval = DefaultReadyPollInterval
	}
	if timeout <= 0 {
		timeout = DefaultReadyTimeout
	}
	return interval, timeout
}

// isClosed reports whether Close has been called.
func (p *DataStreamAudioPublisher) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// Write accepts 24 kHz 16-bit mono little-endian PCM (DefaultOutputSampleRate24k)
// and forwards it to the destination participant, resampling when the stream is
// expected at 16 kHz. The stream is opened lazily on the first write of a segment,
// waiting for the destination participant to join the room if needed.
func (p *DataStreamAudioPublisher) Write(pcm24k []byte) (int, error) {
	if len(pcm24k) < 2 {
		return len(pcm24k), nil
	}

	// Waits outside p.mu so Close can still interrupt the wait.
	if err := p.waitForDestination(); err != nil {
		return 0, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, errors.New("datastream audio publisher is closed")
	}

	payload := pcm24k
	if p.targetSampleRate == DefaultSampleRate16k {
		payload = ResamplePCM16Mono24kTo16k(pcm24k)
	}

	if p.writer == nil {
		writer := p.openStream(lksdk.StreamBytesOptions{
			Topic:                 AudioStreamTopic,
			DestinationIdentities: []string{p.destinationIdentity},
			Attributes: map[string]string{
				"sample_rate":  strconv.Itoa(p.targetSampleRate),
				"num_channels": "1",
			},
		})
		if writer == nil {
			return 0, errors.New("datastream audio publisher: failed to open the byte stream")
		}
		p.writer = writer
	}

	p.writer.Write(payload, nil)
	// payload is in the wire rate (targetSampleRate), while pcm24k is always 24 kHz.
	p.advancePlayoutLocked(len(payload))

	return len(pcm24k), nil
}

// advancePlayoutLocked pushes the playout horizon forward by the duration of the
// queued PCM. The caller must hold p.mu.
func (p *DataStreamAudioPublisher) advancePlayoutLocked(pcmBytes int) {
	channels := p.numChannels
	if channels <= 0 {
		channels = 1
	}
	numFrames := pcmBytes / (2 * channels)
	if numFrames <= 0 || p.targetSampleRate <= 0 {
		return
	}
	duration := time.Duration(numFrames) * time.Second / time.Duration(p.targetSampleRate)

	if now := time.Now(); p.playoutUntil.Before(now) {
		p.playoutUntil = now
	}
	p.playoutUntil = p.playoutUntil.Add(duration)
}

// RemainingDuration reports how much of the written audio has not played yet.
func (p *DataStreamAudioPublisher) RemainingDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playoutUntil.After(time.Now()) {
		return time.Until(p.playoutUntil)
	}
	return 0
}

// EndSegment closes the stream of the current segment. The next Write opens a new
// one, which is what marks a boundary between agent utterances for the client.
func (p *DataStreamAudioPublisher) EndSegment() {
	p.mu.Lock()
	writer := p.writer
	p.writer = nil
	p.mu.Unlock()

	if writer != nil {
		writer.Close()
	}
}

// Flush ends the current segment and asks the destination to drop the audio it
// has buffered. The RPC runs in the background so barge-in never blocks the caller.
func (p *DataStreamAudioPublisher) Flush() {
	p.EndSegment()

	p.mu.Lock()
	p.playoutUntil = time.Time{}
	p.mu.Unlock()

	destinationIdentity := p.destinationIdentity
	performRPC := p.performRPC
	if performRPC == nil {
		return
	}

	go func() {
		timeout := clearBufferRPCTimeout
		_, _ = performRPC(lksdk.PerformRpcParams{
			DestinationIdentity: destinationIdentity,
			Method:              AudioStreamClearMethod,
			ResponseTimeout:     &timeout,
		})
	}()
}

// Close closes the active stream and rejects further writes.
func (p *DataStreamAudioPublisher) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	writer := p.writer
	p.writer = nil
	p.mu.Unlock()

	if writer != nil {
		writer.Close()
	}
}

// ResamplePCM16Mono24kTo16k downsamples 24 kHz 16-bit mono little-endian PCM to
// 16 kHz using a clean 3:2 ratio: every three input samples yield two output
// samples, the first kept as-is and the second the average of the last two.
// A partial tail of one or two input samples yields one output sample.
func ResamplePCM16Mono24kTo16k(pcm24k []byte) []byte {
	numSamples := len(pcm24k) / 2
	if numSamples == 0 {
		return nil
	}

	outSamples := numSamples / 3 * 2
	if numSamples%3 > 0 {
		outSamples++
	}

	out := make([]byte, outSamples*2)

	readSample := func(index int) int16 {
		return int16(binary.LittleEndian.Uint16(pcm24k[index*2:]))
	}
	writeSample := func(index int, sample int16) {
		binary.LittleEndian.PutUint16(out[index*2:], uint16(sample))
	}

	inIndex, outIndex := 0, 0
	for ; inIndex+2 < numSamples; inIndex += 3 {
		s0 := readSample(inIndex)
		s1 := readSample(inIndex + 1)
		s2 := readSample(inIndex + 2)

		writeSample(outIndex, s0)
		writeSample(outIndex+1, int16((int32(s1)+int32(s2))/2))
		outIndex += 2
	}

	if inIndex < numSamples {
		writeSample(outIndex, readSample(inIndex))
	}

	return out
}
