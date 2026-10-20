package insightsservice

import (
	"context"
	"fmt"
	"time"

	"github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

const (
	// Bounded recovery policy for one participant's transcription task.
	transcriptionMaxConnectAttempts = 4
	transcriptionBaseRetryDelay     = 500 * time.Millisecond
	transcriptionReadyTimeout       = 10 * time.Second
	transcriptionHealthyResetAfter  = 30 * time.Second

	// Audio kept while no stream is ready; deliberately small and lossy.
	transcriptionMaxBufferedSamples = 16000 // ~1s of 16kHz mono PCM

	// Consecutive write failures tolerated before the stream counts as dead.
	transcriptionMaxWriteErrors = 30 // matches the original tolerance

	// Delay before the "ready" notice, so a user is not bothered while unmuting.
	transcriptionReadyToastDelay = 5 * time.Second
)

type dialResult struct {
	stream insights.TranscriptionStream
	err    error
}

// transcriptionRun owns one participant's stream for the life of run(); all
// stream transitions happen in that single goroutine, so no locking is needed.
type transcriptionRun struct {
	log         *logrus.Entry
	audioStream <-chan media.PCM16Sample
	wake        <-chan struct{}

	dial          func(context.Context) (insights.TranscriptionStream, error)
	handleEvent   func(*insights.TranscriptionEvent)
	setUsage      func(active bool)
	notifyReady   func()
	notifyStopped func()
	notifyFailed  func()

	retryBaseDelay time.Duration
	readyTimeout   time.Duration
	healthyAfter   time.Duration
	toastDelay     time.Duration

	stream  insights.TranscriptionStream
	results <-chan *insights.TranscriptionEvent
	dialCh  <-chan dialResult

	retryTimer *time.Timer
	retryC     <-chan time.Time
	readyTimer *time.Timer
	readyC     <-chan time.Time
	toastTimer *time.Timer
	toastC     <-chan time.Time

	pending     []int16
	writeErrs   int
	attempts    int
	usageActive bool
	readyAt     time.Time
	toastSent   bool
	failed      bool
}

func newTranscriptionRun(log *logrus.Entry, audioStream <-chan media.PCM16Sample, wake <-chan struct{}, dial func(context.Context) (insights.TranscriptionStream, error)) *transcriptionRun {
	return &transcriptionRun{
		log:            log,
		audioStream:    audioStream,
		wake:           wake,
		dial:           dial,
		handleEvent:    func(*insights.TranscriptionEvent) {},
		setUsage:       func(bool) {},
		notifyReady:    func() {},
		notifyStopped:  func() {},
		notifyFailed:   func() {},
		retryBaseDelay: transcriptionBaseRetryDelay,
		readyTimeout:   transcriptionReadyTimeout,
		healthyAfter:   transcriptionHealthyResetAfter,
		toastDelay:     transcriptionReadyToastDelay,
	}
}

func (r *transcriptionRun) run(ctx context.Context) error {
	// Every exit path cancels an in-flight dial; the producer closes its late stream.
	ctx, cancel := context.WithCancel(ctx)

	defer func() {
		// Cancel a pending dial before any blocking teardown.
		cancel()
		r.cleanup()
		if r.failed {
			r.notifyFailed()
			return
		}
		r.notifyStopped()
	}()

	// An approved task connects immediately, even while muted.
	r.startDial(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case res := <-r.dialCh:
			r.dialCh = nil
			if err := r.onDial(res); err != nil {
				return err
			}

		case <-r.retryC:
			r.clearRetry()
			r.startDial(ctx)

		case <-r.readyC:
			r.clearReadyWait()
			r.log.Warnln("transcription provider did not acknowledge the session in time")
			r.dropStream()
			if err := r.retryOrFail("session was never acknowledged"); err != nil {
				return err
			}

		case <-r.toastC:
			r.clearReadyNotice()
			if !r.toastSent {
				r.toastSent = true
				r.notifyReady()
			}

		case pcm, ok := <-r.audioStream:
			if !ok {
				r.log.Infoln("audio stream closed")
				return nil
			}
			if err := r.onAudio(ctx, pcm); err != nil {
				return err
			}

		case <-r.wake:
			r.maybeStartDial(ctx)

		case event, ok := <-r.results:
			if !ok {
				if err := r.onDisconnect(); err != nil {
					return err
				}
				continue
			}
			if err := r.onEvent(event); err != nil {
				return err
			}
		}
	}
}

// startDial runs at most one background connection attempt.
func (r *transcriptionRun) startDial(ctx context.Context) {
	if r.dialCh != nil {
		return
	}
	ch := make(chan dialResult)
	r.dialCh = ch
	r.writeErrs = 0

	go func() {
		stream, err := r.dial(ctx)
		select {
		case ch <- dialResult{stream: stream, err: err}:
		case <-ctx.Done():
			if stream != nil {
				_ = stream.Close()
			}
		}
	}()
}

func (r *transcriptionRun) maybeStartDial(ctx context.Context) {
	if r.stream != nil || r.dialCh != nil || r.retryC != nil {
		return
	}
	if r.attempts >= transcriptionMaxConnectAttempts {
		return
	}
	r.startDial(ctx)
}

func (r *transcriptionRun) onDial(res dialResult) error {
	if res.err != nil {
		r.log.WithError(res.err).Warnln("failed to connect transcription stream")
		return r.retryOrFail("connect failed")
	}

	r.stream = res.stream
	r.results = res.stream.Results()
	r.writeErrs = 0
	r.readyAt = time.Time{}

	r.readyTimer = time.NewTimer(r.readyTimeout)
	r.readyC = r.readyTimer.C
	return nil
}

func (r *transcriptionRun) onAudio(ctx context.Context, pcm media.PCM16Sample) error {
	if r.stream == nil || r.readyAt.IsZero() {
		// A rejected or still-unready session must never receive task audio.
		r.bufferAudio(pcm)
		r.maybeStartDial(ctx)
		return nil
	}

	if err := r.stream.WriteSample(pcm); err != nil {
		r.writeErrs++
		r.log.WithError(err).Warnf("transcription write failure (%d/%d)", r.writeErrs, transcriptionMaxWriteErrors)
		if r.writeErrs >= transcriptionMaxWriteErrors {
			return r.onDisconnect()
		}
		return nil
	}

	r.writeErrs = 0
	return nil
}

func (r *transcriptionRun) onEvent(event *insights.TranscriptionEvent) error {
	switch event.Type {
	case insights.EventTypeSessionStarted:
		// Ready: start usage and flush the buffered audio in arrival order.
		r.clearReadyWait()
		r.readyAt = time.Now()
		r.startUsage()
		r.flushPending()
		r.armReadyNotice()

	case insights.EventTypeSessionStopped:
		return r.onDisconnect()

	case insights.EventTypeError:
		if r.readyAt.IsZero() {
			// A pre-ready failure is terminal for this attempt (bad config/auth).
			r.log.Errorf("transcription provider error before the session was ready: %s", event.Error)
			r.dropStream()
			return r.retryOrFail("provider rejected the session")
		}
		r.log.Errorln("insights provider error:", event.Error)

	default:
		r.handleEvent(event)
		if event.Type == insights.EventTypeFinalResult || event.Type == insights.EventTypePartialResult {
			// Real recognizer output means the connection is healthy.
			r.attempts = 0
		}
	}

	return nil
}

// onDisconnect releases the dead stream but keeps the task enabled for a retry.
func (r *transcriptionRun) onDisconnect() error {
	if r.stream == nil {
		return nil
	}

	healthy := !r.readyAt.IsZero() && time.Since(r.readyAt) >= r.healthyAfter
	r.dropStream()

	if healthy {
		r.attempts = 0
		return nil
	}

	r.attempts++
	if r.attempts >= transcriptionMaxConnectAttempts {
		r.failed = true
		return fmt.Errorf("%w: provider disconnected %d times", insights.ErrTranscriptionUnrecoverable, r.attempts)
	}
	r.log.WithField("attempt", r.attempts).Warnln("transcription stream disconnected, waiting for audio or unmute")
	return nil
}

// retryOrFail books a failed attempt; only real activity or a healthy stream
// resets the budget.
func (r *transcriptionRun) retryOrFail(reason string) error {
	r.attempts++
	if r.attempts >= transcriptionMaxConnectAttempts {
		r.failed = true
		return fmt.Errorf("%w: %s after %d attempts", insights.ErrTranscriptionUnrecoverable, reason, r.attempts)
	}

	delay := r.retryBaseDelay << (r.attempts - 1)
	r.log.WithField("delay", delay.String()).Warnln("retrying transcription connection")
	r.retryTimer = time.NewTimer(delay)
	r.retryC = r.retryTimer.C
	return nil
}

// dropStream stops usage, closes the current stream and drains its results.
func (r *transcriptionRun) dropStream() {
	r.clearReadyWait()
	r.clearReadyNotice()
	r.stopUsage()
	r.readyAt = time.Time{}
	r.writeErrs = 0

	stream, results := r.stream, r.results
	r.stream, r.results = nil, nil
	if stream == nil {
		return
	}

	_ = stream.Close()

	if results == nil {
		return
	}
	for event := range results {
		if event.Type == insights.EventTypeFinalResult || event.Type == insights.EventTypePartialResult {
			r.handleEvent(event)
		}
	}
}

func (r *transcriptionRun) cleanup() {
	r.clearRetry()
	r.dropStream()
}

func (r *transcriptionRun) bufferAudio(pcm media.PCM16Sample) {
	if len(pcm) == 0 {
		return
	}
	// Copy: the media pipeline may reuse the sample buffer.
	r.pending = append(r.pending, pcm...)
	if over := len(r.pending) - transcriptionMaxBufferedSamples; over > 0 {
		r.pending = append(r.pending[:0], r.pending[over:]...)
	}
}

func (r *transcriptionRun) flushPending() {
	if r.stream == nil || len(r.pending) == 0 {
		return
	}
	pending := r.pending
	r.pending = nil
	if err := r.stream.WriteSample(pending); err != nil {
		r.log.WithError(err).Warnln("failed to write buffered audio to the new transcription stream")
	}
}

func (r *transcriptionRun) startUsage() {
	if r.usageActive {
		return
	}
	r.usageActive = true
	r.setUsage(true)
}

func (r *transcriptionRun) stopUsage() {
	if !r.usageActive {
		return
	}
	r.usageActive = false
	r.setUsage(false)
}

// armReadyNotice schedules the ready notice once per task. dropStream cancels
// it while disconnected, so it only fires for a ready, connected stream.
func (r *transcriptionRun) armReadyNotice() {
	if r.toastSent || r.toastTimer != nil {
		return
	}
	r.toastTimer = time.NewTimer(r.toastDelay)
	r.toastC = r.toastTimer.C
}

func (r *transcriptionRun) clearRetry() {
	if r.retryTimer != nil {
		r.retryTimer.Stop()
	}
	r.retryTimer, r.retryC = nil, nil
}

func (r *transcriptionRun) clearReadyWait() {
	if r.readyTimer != nil {
		r.readyTimer.Stop()
	}
	r.readyTimer, r.readyC = nil, nil
}

func (r *transcriptionRun) clearReadyNotice() {
	if r.toastTimer != nil {
		r.toastTimer.Stop()
	}
	r.toastTimer, r.toastC = nil, nil
}
