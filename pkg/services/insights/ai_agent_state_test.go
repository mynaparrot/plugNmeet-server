package insightsservice

import (
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func waitForAgentState(t *testing.T, env *agentTestEnv, want plugnmeet.AIAgentRuntimeState, what string) {
	t.Helper()
	waitFor(t, func() bool {
		return env.transport.lastState() == want
	}, what)
}

// The turn-complete arrives before the queued audio reached the listener, so the
// status must stay SPEAKING until the playout drained.
func TestAIAgentHoldsSpeakingUntilPlayoutDrains(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(120 * time.Millisecond)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	time.Sleep(50 * time.Millisecond)
	if got := env.transport.lastState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING {
		t.Fatalf("the state must stay SPEAKING while the audio plays out, got %s", got)
	}

	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the playout drained")
}

// A THINKING report that arrives while the filler acknowledgement is still being
// heard must not cut the audio: SPEAKING holds, then the status moves to THINKING
// instead of LISTENING once the playout drained.
func TestAIAgentDefersThinkingUntilPlayoutDrains(t *testing.T) {
	previous := aiAgentSpeakingIdle
	aiAgentSpeakingIdle = 100 * time.Millisecond
	t.Cleanup(func() { aiAgentSpeakingIdle = previous })

	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(250 * time.Millisecond)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")
	segments := env.publisher.segmentCount()

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	time.Sleep(100 * time.Millisecond)
	if got := env.transport.lastState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING {
		t.Fatalf("the state must stay SPEAKING while the filler plays out, got %s", got)
	}

	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "thinking after the playout drained")
	// The filler segment stays open: only a completed turn closes it, so an
	// avatar stream is never chopped mid-sentence.
	if got := env.publisher.segmentCount(); got != segments {
		t.Fatalf("segment ends = %d, want %d before the turn completed", got, segments)
	}

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state of the answer")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the turn completed")
}

// A tool that finished while its audio still plays must not leave the name in
// progress: the THINKING the provider reported for the turn the result resumes
// takes over once the playout drained.
func TestAIAgentDefersToolThinkingUntilPlayoutDrains(t *testing.T) {
	previous := aiAgentSpeakingIdle
	aiAgentSpeakingIdle = 100 * time.Millisecond
	t.Cleanup(func() { aiAgentSpeakingIdle = previous })

	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(250 * time.Millisecond)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")

	env.agent.setToolState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING, "get_poll_results")
	// The provider reports the follow-up turn before the result is handed back.
	env.agent.markThinking()
	env.agent.finishToolCall("get_poll_results")

	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "thinking after the finished tool drained")
	if msg := env.transport.lastStateMsg(); msg.GetToolInProgress() != "" {
		t.Fatalf("a finished tool must not stay in progress, got %s", msg.GetToolInProgress())
	}
}

// A tool result that resumes no turn must not leave the status on THINKING: the
// speech of the turn that called the tool keeps it, then LISTENING.
func TestAIAgentFinishToolCallFallsBackToListening(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.agent.setToolState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "notepad_write")
	env.agent.finishToolCall("notepad_write")

	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the silent tool result")
	if msg := env.transport.lastStateMsg(); msg.GetToolInProgress() != "" {
		t.Fatalf("a finished tool must not stay in progress, got %s", msg.GetToolInProgress())
	}
}

// The turn that called the tool is still being heard, so the cleared label
// leaves SPEAKING to the audio and falls back to LISTENING once it drained.
func TestAIAgentFinishToolCallKeepsSpeakingUntilPlayoutDrains(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(200 * time.Millisecond)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")

	env.agent.setToolState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING, "whiteboard_draw_diagram")
	env.agent.finishToolCall("whiteboard_draw_diagram")

	msg := env.transport.lastStateMsg()
	if msg.GetState() != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING || msg.GetToolInProgress() != "" {
		t.Fatalf("a finished tool must give way to the speech, got state=%s tool=%q", msg.GetState(), msg.GetToolInProgress())
	}

	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the playout drained")
}

// A tool call that arrives while the announcing sentence is still playing must
// not cut the speech short: the label becomes the tool, speaking holds until the
// queued audio drained.
func TestAIAgentToolCallKeepsSpeakingUntilPlayoutDrains(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(300 * time.Millisecond)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")

	env.agent.setToolState(plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING, "whiteboard_write_content")
	if msg := env.transport.lastStateMsg(); !msg.GetIsSpeaking() || msg.GetToolInProgress() != "whiteboard_write_content" {
		t.Fatalf("the tool label must keep the queued speech, got speaking=%v tool=%q", msg.GetIsSpeaking(), msg.GetToolInProgress())
	}

	waitFor(t, func() bool {
		return !env.transport.lastStateMsg().GetIsSpeaking()
	}, "speaking cleared after the playout drained")
	if got := env.transport.lastState(); got != plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_DRAWING {
		t.Fatalf("the tool state must survive the playout, got %s", got)
	}
}

func TestAIAgentInterruptedFlushesPlayout(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.publisher.setPlayout(2 * time.Second)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening right after barge-in")

	if _, flushes := env.publisher.snapshot(); flushes != 1 {
		t.Fatalf("barge-in must flush the queued audio, got %d flushes", flushes)
	}
	if rem := env.publisher.RemainingDuration(); rem != 0 {
		t.Fatalf("barge-in must drop the remaining playout, got %s", rem)
	}
}

func TestAIAgentThinkingTransitions(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "thinking state")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING, "speaking state on the first audio chunk")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the turn completed")
}

// A provider that never emits audio nor a turn-complete must not leave the
// status stuck on THINKING.
func TestAIAgentThinkingFallsBackToListening(t *testing.T) {
	previous := aiAgentThinkingIdle
	aiAgentThinkingIdle = 200 * time.Millisecond
	t.Cleanup(func() { aiAgentThinkingIdle = previous })

	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "thinking state")
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the thinking timeout")
}
