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
	// clearBufferRPCTimeout bounds the clear RPC so a silent client cannot leave
	// the call hanging.
	clearBufferRPCTimeout = 1500 * time.Millisecond
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

	openStream openByteStreamFunc
	performRPC performRPCFunc

	mu     sync.Mutex
	writer byteStreamWriter
	closed bool
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
	return &DataStreamAudioPublisher{
		destinationIdentity: destinationIdentity,
		targetSampleRate:    targetSampleRate,
		openStream: func(options lksdk.StreamBytesOptions) byteStreamWriter {
			return localParticipant.StreamBytes(options)
		},
		performRPC: func(params lksdk.PerformRpcParams) (*string, error) {
			return localParticipant.PerformRpc(params)
		},
	}, nil
}

// Write accepts 24 kHz 16-bit mono little-endian PCM (DefaultOutputSampleRate24k)
// and forwards it to the destination participant, resampling when the stream is
// expected at 16 kHz. The stream is opened lazily on the first write of a segment.
func (p *DataStreamAudioPublisher) Write(pcm24k []byte) (int, error) {
	if len(pcm24k) < 2 {
		return len(pcm24k), nil
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

	return len(pcm24k), nil
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
