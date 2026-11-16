package media

import "testing"

func TestAudioPublisherNilTrack(t *testing.T) {
	p := &AudioPublisher{}

	p.Flush() // must be a no-op rather than a panic
	p.Close()

	if err := p.WriteSample(nil); err == nil {
		t.Fatal("expected an error for an uninitialized publisher")
	}
}

func TestAudioPublisherSampleRates(t *testing.T) {
	if DefaultSampleRate16k != 16000 {
		t.Fatalf("unexpected TTS sample rate: %d", DefaultSampleRate16k)
	}
	if DefaultOutputSampleRate24k != 24000 {
		t.Fatalf("unexpected realtime output sample rate: %d", DefaultOutputSampleRate24k)
	}
}
