package insightsservice

import (
	"context"
	"errors"
	"strings"
	"time"

	mediasdk "github.com/livekit/media-sdk"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// pushAudio queues participant audio, dropping the oldest chunk on overflow so
// the RTP reader is never blocked.
func (a *AIAgent) pushAudio(sample mediasdk.PCM16Sample) {
	select {
	case a.inAudio <- sample:
		return
	default:
	}
	select {
	case <-a.inAudio:
	default:
	}
	select {
	case a.inAudio <- sample:
	default:
	}
}

func (a *AIAgent) writeAudioLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case sample, ok := <-a.inAudio:
			if !ok {
				return
			}
			if a.paused.Load() {
				continue
			}
			session := a.currentSession()
			if session == nil {
				continue
			}
			if err := session.SendAudio(pcm16SampleToBytes(sample)); err != nil {
				a.logger.WithError(err).Debug("ai agent: failed to send participant audio")
			}
		}
	}
}

func (a *AIAgent) readEventLoop(loop *aiAgentLoop) {
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-loop.stop:
			return
		case ev, ok := <-loop.events:
			if !ok {
				return
			}
			a.handleEvent(loop.session, ev)
		}
	}
}

func (a *AIAgent) handleEvent(session insights.RealtimeSession, ev insights.RealtimeEvent) {
	switch ev.Type {
	case insights.RealtimeEventAudioDelta:
		if a.paused.Load() || len(ev.AudioPCM24k) == 0 {
			return
		}
		if a.publisher != nil {
			if _, err := a.publisher.Write(ev.AudioPCM24k); err != nil {
				a.logger.WithError(err).Debug("ai agent: failed to write agent audio")
			}
		}
		a.markSpeaking()

	case insights.RealtimeEventInterrupted:
		// Barge-in: every queued millisecond is a UX bug.
		if a.publisher != nil {
			a.publisher.Flush()
		}
		a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")

	case insights.RealtimeEventTranscriptUser:
		a.recordUserTranscript(ev)

	case insights.RealtimeEventTranscriptAgent:
		if ev.Final && strings.TrimSpace(ev.Text) != "" {
			a.recordTranscriptTurn(transcriptRoleAgent, ev.Text)
		}
		if ev.Final && a.guard.CheckAgentTranscript(ev.Text) {
			a.logger.WithField("strikes", a.guard.OffTopicRedirectsCount()).Warn("ai agent: agent transcript drifted from the boundary")
		}

	case insights.RealtimeEventTurnComplete:
		// A turn that ends while paused must not flip the paused state back.
		if !a.paused.Load() {
			a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
		}
		if inject, anchor := a.guard.OnTurnComplete(); inject {
			if err := a.sendContextText(anchor); err != nil {
				a.logger.WithError(err).Debug("ai agent: failed to inject the topic anchor")
			}
		}

	case insights.RealtimeEventToolCall:
		a.handleToolCall(session, ev)

	case insights.RealtimeEventGoAway:
		a.logger.Info("ai agent: provider will close the session, redialling")
		go a.redial()

	case insights.RealtimeEventUsage:
		a.recordUsage(ev)

	case insights.RealtimeEventError:
		if ev.Err != nil {
			a.logger.WithError(ev.Err).Error("ai agent: session error")
		}
		// Never stop from a loop goroutine: Stop waits for the loops to finish.
		go a.Stop("error")
	}
}

// redial replaces a session the provider is about to close, keeping the LiveKit
// participant and the room state untouched.
func (a *AIAgent) redial() {
	if a.newSession == nil {
		return
	}
	if budget := a.sessionBudget(); budget > 0 && time.Since(a.startedAt) >= budget {
		a.Stop("budget")
		return
	}

	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_RECONNECTING, "goaway")
	session, err := a.newSession(a.ctx)
	if err != nil {
		a.logger.WithError(err).Error("ai agent: redial failed")
		a.Stop("error")
		return
	}

	a.loopMu.Lock()
	loop := a.trackLoop(session)
	a.loopMu.Unlock()

	a.sessionMu.Lock()
	old := a.session
	a.session = session
	a.sessionMu.Unlock()

	if old != nil {
		if err := old.Close(); err != nil {
			a.logger.WithError(err).Debug("ai agent: failed to close the replaced session")
		}
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.readEventLoop(loop)
	}()

	a.setState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "")
	a.logger.Info("ai agent: redialled the provider session")
}

// trackLoop installs loop as the current event loop and retires the previous one.
// The caller must hold loopMu.
func (a *AIAgent) trackLoop(session insights.RealtimeSession) *aiAgentLoop {
	loop := &aiAgentLoop{session: session, events: session.Events(), stop: make(chan struct{})}
	if a.loop != nil {
		close(a.loop.stop)
	}
	a.loop = loop
	return loop
}

func (a *AIAgent) currentSession() insights.RealtimeSession {
	a.sessionMu.RLock()
	defer a.sessionMu.RUnlock()
	return a.session
}

func (a *AIAgent) sendContextText(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	session := a.currentSession()
	if session == nil {
		return errors.New("ai agent: no active session")
	}
	return session.SendContextText("user", text, false)
}

func (a *AIAgent) sessionBudget() time.Duration {
	seconds := a.features.GetMaxSessionSeconds()
	if seconds == 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// aiAgentAudioTask forwards a participant's transcoded PCM into the agent's
// bounded input queue. It is the insights.Task the RoomAgent pipelines run.
type aiAgentAudioTask struct {
	sink func(mediasdk.PCM16Sample)
}

func (t *aiAgentAudioTask) RunAudioStream(ctx context.Context, stream <-chan mediasdk.PCM16Sample, muted <-chan bool, _ uint64, _, _ string, _ []byte) error {
	var isMuted bool
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case state, ok := <-muted:
			if !ok {
				muted = nil
				continue
			}
			isMuted = state
		case sample, ok := <-stream:
			if !ok {
				return nil
			}
			if !isMuted && t.sink != nil {
				t.sink(sample)
			}
		}
	}
}

func (t *aiAgentAudioTask) RunStateless(context.Context, []byte) (interface{}, error) {
	return nil, errors.New("ai agent: stateless tasks are not supported")
}

// pcm16SampleToBytes renders 16-bit mono samples as little-endian PCM bytes.
func pcm16SampleToBytes(sample mediasdk.PCM16Sample) []byte {
	out := make([]byte, len(sample)*2)
	for i, v := range sample {
		out[i*2] = byte(v)
		out[i*2+1] = byte(uint16(v) >> 8)
	}
	return out
}
