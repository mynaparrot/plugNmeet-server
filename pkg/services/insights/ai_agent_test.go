package insightsservice

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/livekit/media-sdk"
	lkauth "github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/fake"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type recordingPublisher struct {
	mu           sync.Mutex
	written      []byte
	flushes      int
	segments     int
	closed       bool
	playoutUntil time.Time
}

func (p *recordingPublisher) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, data...)
	return len(data), nil
}

// EndSegment records how often the agent closed an utterance, so the tests can
// assert that a turn boundary reaches the publisher.
func (p *recordingPublisher) EndSegment() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.segments++
}

func (p *recordingPublisher) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushes++
	p.playoutUntil = time.Time{}
}

// setPlayout fakes audio queued for duration.
func (p *recordingPublisher) setPlayout(duration time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playoutUntil = time.Now().Add(duration)
}

func (p *recordingPublisher) RemainingDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playoutUntil.After(time.Now()) {
		return time.Until(p.playoutUntil)
	}
	return 0
}

func (p *recordingPublisher) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

func (p *recordingPublisher) snapshot() (written []byte, flushes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.written...), p.flushes
}

func (p *recordingPublisher) segmentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.segments
}

type recordingTransport struct {
	mu     sync.Mutex
	states []*plugnmeet.AIAgentStateMsg
}

func (t *recordingTransport) BroadcastAIAgentState(_ string, msg *plugnmeet.AIAgentStateMsg) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states = append(t.states, msg)
	return nil
}

func (t *recordingTransport) lastState() plugnmeet.AIAgentRuntimeState {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.states) == 0 {
		return plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_UNSPECIFIED
	}
	return t.states[len(t.states)-1].GetState()
}

func (t *recordingTransport) lastStateMsg() *plugnmeet.AIAgentStateMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.states) == 0 {
		return nil
	}
	return t.states[len(t.states)-1]
}

type agentTestEnv struct {
	agent     *AIAgent
	session   *fake.Session
	publisher *recordingPublisher
	transport *recordingTransport
}

func newAgentTestEnv(t *testing.T, registry *mcp.Registry, modify func(*aiAgentCoreArgs)) *agentTestEnv {
	t.Helper()

	session := fake.NewSession()
	transport := &recordingTransport{}
	publisher := &recordingPublisher{}

	args := &aiAgentCoreArgs{
		Ctx:      context.Background(),
		Payload:  &insights.InsightsTaskPayload{RoomId: "room-" + t.Name(), ServiceType: insights.ServiceTypeAIAgent},
		Features: &plugnmeet.InsightsAIAgentFeatures{},
		Boundary: BoundaryConfig{
			AgentDisplayName:    "Ms. Ava",
			Topic:               "Grade 4 Fractions",
			StrictEnforcement:   true,
			AnchorIntervalTurns: 3,
		},
		Session:     session,
		Transport:   transport,
		Logger:      logrus.NewEntry(logrus.StandardLogger()),
		Registry:    registry,
		SessionId:   "session-1",
		AgentUserId: "pnm_agent-ai_agent-test",
	}
	if modify != nil {
		modify(args)
	}

	agent, err := newAIAgent(args)
	if err != nil {
		t.Fatalf("newAIAgent: %v", err)
	}
	agent.publisher = publisher
	agent.Start()
	t.Cleanup(func() { agent.Stop("test_done") })

	return &agentTestEnv{agent: agent, session: session, publisher: publisher, transport: transport}
}

func waitFor(t *testing.T, check func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

// echoTool is a server tool that returns the arguments it received.
func echoTool(name string) *mcp.Tool {
	return &mcp.Tool{
		Kind: mcp.ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name:        name,
			Description: "echo",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(_ context.Context, call mcp.ToolCall) (mcp.ToolResult, error) {
			return mcp.ToolResult{OutputJSON: `{"echo":true}`}, nil
		},
	}
}

// The LiveKit identity must be the id the model layer registered in NATS;
// browsers look the agent up by it when its audio track arrives.
func TestNewAIAgentKeepsProvidedAgentUserId(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)
	if got := env.agent.AgentUserID(); got != "pnm_agent-ai_agent-test" {
		t.Fatalf("expected the provided agent user id, got %q", got)
	}
}

func TestAIAgentAudioDeltaWritesToPublisherAndMarksSpeaking(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})

	waitFor(t, func() bool {
		written, _ := env.publisher.snapshot()
		return len(written) == 4
	}, "agent audio written")
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	}, "speaking state broadcast")
}

// A provider that streams audio and then goes quiet without a turn-complete
// must not leave the status pill on SPEAKING.
func TestAIAgentSpeakingFallsBackToListeningWhenAudioStops(t *testing.T) {
	previous := aiAgentSpeakingIdle
	aiAgentSpeakingIdle = 30 * time.Millisecond
	t.Cleanup(func() { aiAgentSpeakingIdle = previous })

	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4}})
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_SPEAKING
	}, "speaking state broadcast")
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
	}, "listening state after the audio stopped")
}

func TestAIAgentBargeInFlushesQueuedAudio(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(
		insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{1, 2, 3, 4, 5, 6}},
		insights.RealtimeEvent{Type: insights.RealtimeEventInterrupted},
	)

	waitFor(t, func() bool {
		_, flushes := env.publisher.snapshot()
		return flushes == 1
	}, "publisher flush on barge-in")
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
	}, "listening state after barge-in")
}

func TestAIAgentToolCallRunsExecutorAndAnswersProvider(t *testing.T) {
	registry := mcp.NewRegistry()
	registry.Register(echoTool("whiteboard_write_content"))
	env := newAgentTestEnv(t, registry, nil)

	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-1",
		ToolName: "whiteboard_write_content",
		ArgsJSON: `{}`,
	})

	waitFor(t, func() bool {
		return len(env.session.ToolResults()) == 1
	}, "tool result sent")

	result := env.session.ToolResults()[0]
	if result.CallID != "call-1" || result.IsError || !strings.Contains(result.ResultJSON, "echo") {
		t.Fatalf("unexpected tool result: %+v", result)
	}
	// The answer is the provider's decision: the fake one resumes no turn, so
	// the finished tool only gives up its label instead of forcing THINKING.
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING, "listening after the tool result")
	if msg := env.transport.lastStateMsg(); msg.GetToolInProgress() != "" {
		t.Fatalf("a finished tool must give way, got tool %q", msg.GetToolInProgress())
	}

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventThinking})
	waitForAgentState(t, env, plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING, "thinking state while the answer is composed")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
	}, "listening after the turn completed")
}

func TestAIAgentReportOffTopicRecordsStrike(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-2",
		ToolName: toolNameReportOffTopic,
		ArgsJSON: `{"category":"unrelated_subject","user_request_summary":"asked about an election"}`,
	})

	waitFor(t, func() bool {
		return len(env.session.ToolResults()) == 1
	}, "off-topic tool result")

	result := env.session.ToolResults()[0]
	if result.ToolName != toolNameReportOffTopic || result.IsError {
		t.Fatalf("unexpected tool result: %+v", result)
	}
	if !strings.Contains(result.ResultJSON, `"recorded":true`) || !strings.Contains(result.ResultJSON, `"strikes":1`) {
		t.Fatalf("strikes were not reported: %s", result.ResultJSON)
	}
	if got := env.agent.guard.OffTopicRedirectsCount(); got != 1 {
		t.Fatalf("expected 1 recorded off-topic attempt, got %d", got)
	}
}

func TestAIAgentTurnCompleteInjectsAnchor(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	for i := 0; i < 3; i++ {
		env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	}

	waitFor(t, func() bool {
		for _, injected := range env.session.Contexts() {
			if strings.Contains(injected.Text, "[SYSTEM ANCHOR") && strings.Contains(injected.Text, "Grade 4 Fractions") {
				return true
			}
		}
		return false
	}, "topic anchor injection")
}

func TestAIAgentToolTurnDoesNotAdvanceAnchorCadence(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	// A tool-call turn ends silently, so it must not count as a spoken turn.
	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-1",
		ToolName: "get_poll_results",
		ArgsJSON: `{}`,
	})
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})

	waitFor(t, func() bool {
		return env.agent.guard.TurnCount() == 0
	}, "the tool turn to be skipped")
	for _, injected := range env.session.Contexts() {
		if strings.Contains(injected.Text, "[SYSTEM ANCHOR") {
			t.Fatalf("a silent tool turn must not inject the anchor: %q", injected.Text)
		}
	}

	for i := 0; i < 3; i++ {
		env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	}

	waitFor(t, func() bool {
		for _, injected := range env.session.Contexts() {
			if strings.Contains(injected.Text, "[SYSTEM ANCHOR") && strings.Contains(injected.Text, "TURN 3") {
				return true
			}
		}
		return false
	}, "the anchor after three spoken turns")

	if got := env.agent.guard.TurnCount(); got != 3 {
		t.Fatalf("TurnCount = %d, want 3", got)
	}
}

func TestAIAgentPauseAndResume(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.Pause(); err != nil {
		t.Fatalf("pause: %v", err)
	}
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{9, 9, 9, 9}})
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_PAUSED
	}, "paused state")

	if written, _ := env.publisher.snapshot(); len(written) != 0 {
		t.Fatalf("paused agent must not write audio, wrote %d bytes", len(written))
	}

	if err := env.agent.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitFor(t, func() bool {
		return len(env.session.Contexts()) > 0
	}, "anchor injected on resume")
}

func TestAIAgentWhiteboardSceneBecomesUntrustedImage(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	err := env.agent.HandleWhiteboardScene(&plugnmeet.AIAgentWhiteboardSceneMsg{
		RoomId:         "room",
		Reason:         "participant_edit",
		WebpImage:      []byte{0x52, 0x49, 0x46, 0x46},
		SceneGraphJson: `{"elements":[]}`,
	})
	if err != nil {
		t.Fatalf("handle whiteboard scene: %v", err)
	}

	images := env.session.Images()
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if images[0].MIMEType != "image/webp" {
		t.Fatalf("unexpected mime type: %s", images[0].MIMEType)
	}
	if !strings.Contains(images[0].Caption, untrustedWorkspaceTag) {
		t.Fatalf("caption was not framed as untrusted: %s", images[0].Caption)
	}
}

func TestAIAgentWhiteboardImageWithoutMetadataIsFlagged(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	err := env.agent.HandleWhiteboardScene(&plugnmeet.AIAgentWhiteboardSceneMsg{
		RoomId:    "room",
		Reason:    "page_change",
		WebpImage: []byte{0x52, 0x49, 0x46, 0x46},
	})
	if err != nil {
		t.Fatalf("handle whiteboard scene: %v", err)
	}

	images := env.session.Images()
	if len(images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(images))
	}
	if !strings.Contains(images[0].Caption, untrustedWorkspaceTag) {
		t.Fatalf("caption was not framed as untrusted: %s", images[0].Caption)
	}
	if !strings.Contains(images[0].Caption, "without element metadata") {
		t.Fatalf("an image without metadata must be flagged: %s", images[0].Caption)
	}
}

func TestAIAgentWhiteboardSceneWithoutImageSendsContextText(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	err := env.agent.HandleWhiteboardScene(&plugnmeet.AIAgentWhiteboardSceneMsg{
		RoomId: "room",
		Reason: "tool_request",
		SceneGraphJson: `{"elements":[` +
			`{"id":"ai_1","type":"text","author":"agent","text":"Prompt: write the past tense","x":0,"y":0},` +
			`{"id":"el_1","type":"text","author":"participant","text":"I goed to school","x":0,"y":40}]}`,
	})
	if err != nil {
		t.Fatalf("handle whiteboard scene: %v", err)
	}

	if images := env.session.Images(); len(images) != 0 {
		t.Fatalf("a scene-graph-only snapshot must not send an image, got %d", len(images))
	}

	contexts := env.session.Contexts()
	if len(contexts) != 1 {
		t.Fatalf("expected 1 injected context, got %d", len(contexts))
	}
	if contexts[0].Role != "user" || contexts[0].TriggerResponse {
		t.Fatalf("unexpected context role/trigger: %+v", contexts[0])
	}
	if !strings.Contains(contexts[0].Text, untrustedWorkspaceTag) {
		t.Fatalf("scene graph was not framed as untrusted: %s", contexts[0].Text)
	}
	if !strings.Contains(contexts[0].Text, "I goed to school") ||
		!strings.Contains(contexts[0].Text, "Prompt: write the past tense") {
		t.Fatalf("scene graph authorship was lost: %s", contexts[0].Text)
	}
}

func TestAIAgentWhiteboardSceneIgnoresEmptySnapshot(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.HandleWhiteboardScene(nil); err != nil {
		t.Fatalf("nil message: %v", err)
	}
	if err := env.agent.HandleWhiteboardScene(&plugnmeet.AIAgentWhiteboardSceneMsg{RoomId: "room"}); err != nil {
		t.Fatalf("empty message: %v", err)
	}

	if contexts, images := env.session.Contexts(), env.session.Images(); len(contexts) != 0 || len(images) != 0 {
		t.Fatalf("empty snapshot must be ignored, got %d contexts and %d images", len(contexts), len(images))
	}
}

func TestAIAgentParticipantChatMessageTriggersAnAnswer(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.HandleParticipantChatMessage("user-7", "Sam", "How do I spell necessary?"); err != nil {
		t.Fatalf("handle participant chat message: %v", err)
	}

	contexts := env.session.Contexts()
	if len(contexts) != 1 {
		t.Fatalf("expected 1 injected context, got %d", len(contexts))
	}
	if contexts[0].Role != "user" || !contexts[0].TriggerResponse {
		t.Fatalf("a chat message must trigger an answer: %+v", contexts[0])
	}
	if !strings.Contains(contexts[0].Text, participantChatTag) ||
		!strings.Contains(contexts[0].Text, "> How do I spell necessary?") {
		t.Fatalf("chat message was not framed as untrusted data: %s", contexts[0].Text)
	}

	// A typed message counts as a participant turn in the telemetry.
	snapshot := env.agent.TelemetrySnapshot("")
	if snapshot.TurnsCount != 1 {
		t.Errorf("turnsCount = %d, want 1", snapshot.TurnsCount)
	}
	if len(snapshot.Transcript) != 1 || snapshot.Transcript[0].Role != "participant" ||
		snapshot.Transcript[0].Text != "How do I spell necessary?" {
		t.Fatalf("the typed chat message was not recorded: %+v", snapshot.Transcript)
	}

	if err := env.agent.HandleParticipantChatMessage("user-7", "Sam", "   "); err == nil {
		t.Fatal("an empty chat message must be rejected")
	}
	if contexts := env.session.Contexts(); len(contexts) != 1 {
		t.Fatalf("an empty chat message must not reach the provider, got %d contexts", len(contexts))
	}
	if snapshot := env.agent.TelemetrySnapshot(""); snapshot.TurnsCount != 1 || len(snapshot.Transcript) != 1 {
		t.Fatalf("an empty chat message must not be recorded: %+v", snapshot)
	}

	env.agent.Stop("test_done")
	if err := env.agent.HandleParticipantChatMessage("user-7", "Sam", "still there?"); err == nil {
		t.Fatal("a stopped agent must reject chat messages")
	}
}

func TestAIAgentSendProactiveContextTriggersAnAnswer(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.SendProactiveContext("The countdown timer has finished.", "countdown_timer"); err != nil {
		t.Fatalf("send proactive context: %v", err)
	}

	contexts := env.session.Contexts()
	if len(contexts) != 1 {
		t.Fatalf("expected 1 injected context, got %d", len(contexts))
	}
	if contexts[0].Role != "user" || !contexts[0].TriggerResponse {
		t.Fatalf("a proactive context must trigger an answer: %+v", contexts[0])
	}
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_THINKING
	}, "thinking state while the triggered answer is composed")
	if !strings.Contains(contexts[0].Text, "[SYSTEM CONTEXT — countdown_timer]") ||
		!strings.Contains(contexts[0].Text, "The countdown timer has finished.") {
		t.Fatalf("unexpected proactive context: %s", contexts[0].Text)
	}

	if err := env.agent.SendProactiveContext("   ", "countdown_timer"); err != nil {
		t.Fatalf("empty proactive context: %v", err)
	}
	if contexts := env.session.Contexts(); len(contexts) != 1 {
		t.Fatalf("an empty proactive context must be ignored, got %d contexts", len(contexts))
	}

	// The note is kept in the transcript as context, and it is not a participant
	// turn: only speech and typed messages count.
	snapshot := env.agent.TelemetrySnapshot("")
	if snapshot.TurnsCount != 0 {
		t.Errorf("turnsCount = %d, want 0", snapshot.TurnsCount)
	}
	if len(snapshot.Transcript) != 1 || snapshot.Transcript[0].Role != "context" ||
		!strings.Contains(snapshot.Transcript[0].Text, "The countdown timer has finished.") {
		t.Fatalf("the proactive context was not recorded: %+v", snapshot.Transcript)
	}
}

func TestAIAgentAudioInputIsForwardedToProvider(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	if err := env.agent.ctx.Err(); err != nil {
		t.Fatalf("agent context already done: %v", err)
	}
	env.agent.pushAudio(media.PCM16Sample{1000, -1000})

	waitFor(t, func() bool {
		return len(env.session.AudioChunks()) == 1
	}, "participant audio forwarded")

	chunk := env.session.AudioChunks()[0]
	if len(chunk) != 4 {
		t.Fatalf("unexpected chunk length: %d", len(chunk))
	}
	want := []byte{0xE8, 0x03, 0x18, 0xFC} // little-endian 1000 and -1000
	for i := range want {
		if chunk[i] != want[i] {
			t.Fatalf("unexpected pcm bytes: %v", chunk)
		}
	}
}

func TestAIAgentRedialsOnGoAway(t *testing.T) {
	next := fake.NewSession()
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.NewSession = func(context.Context) (insights.RealtimeSession, error) { return next, nil }
	})

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventGoAway})

	waitFor(t, func() bool {
		return env.agent.currentSession() == next
	}, "session replaced on go away")

	// The new session drives the audio path.
	next.Push(insights.RealtimeEvent{Type: insights.RealtimeEventAudioDelta, AudioPCM24k: []byte{7, 7}})
	waitFor(t, func() bool {
		written, _ := env.publisher.snapshot()
		return len(written) == 2
	}, "agent audio from the redialled session")
}

func TestAIAgentRegistersAndRemovesItself(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	found, ok := GetActiveAIAgent(env.agent.RoomID())
	if !ok || found != env.agent {
		t.Fatal("agent was not registered as the room's active agent")
	}

	env.agent.Stop("test_done")
	waitFor(t, func() bool {
		_, still := GetActiveAIAgent(env.agent.RoomID())
		return !still
	}, "agent unregistered")

	select {
	case <-env.agent.Done():
	default:
		t.Fatal("Done channel was not closed after Stop")
	}
}

func TestAIAgentStopsOnSessionError(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	// A terminal provider error must stop the agent without deadlocking on the
	// event loop that reported it.
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventError, Err: context.DeadlineExceeded})

	select {
	case <-env.agent.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not stop after a session error")
	}
}

func TestAIAgentTelemetrySnapshot(t *testing.T) {
	registry := mcp.NewRegistry()
	registry.Register(echoTool("whiteboard_write_content"))
	env := newAgentTestEnv(t, registry, nil)

	env.session.Push(
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "what is a quarter", Final: true},
		// Partials and blanks are not turns.
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "what is a qu", Final: false},
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "   ", Final: true},
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: "one of four equal parts", Final: true},
		insights.RealtimeEvent{Type: insights.RealtimeEventUsage, InputTokens: 120, OutputTokens: 40, AudioMillis: 2500},
		insights.RealtimeEvent{Type: insights.RealtimeEventUsage, DelegationInputTokens: 300, DelegationOutputTokens: 90},
	)
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").TurnsCount == 1
	}, "participant turn counted")

	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-t1",
		ToolName: "whiteboard_write_content",
		ArgsJSON: `{}`,
	})
	waitFor(t, func() bool {
		return len(env.session.ToolResults()) == 1
	}, "tool result sent")

	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-t2",
		ToolName: toolNameReportOffTopic,
		ArgsJSON: `{"category":"unrelated_subject","user_request_summary":"asked about an election"}`,
	})
	waitFor(t, func() bool {
		return env.agent.guard.OffTopicRedirectsCount() == 1
	}, "off-topic redirect recorded")

	snapshot := env.agent.TelemetrySnapshot("budget")
	if snapshot.SessionID != "session-1" || snapshot.AgentName != "Ms. Ava" {
		t.Fatalf("unexpected session identity: %+v", snapshot)
	}
	if snapshot.Topic != "Grade 4 Fractions" || len(snapshot.AllowedSubtopics) != 0 {
		t.Fatalf("unexpected topic: %q (%v)", snapshot.Topic, snapshot.AllowedSubtopics)
	}
	if snapshot.StartedAt != env.agent.startedAt.UnixMilli() || snapshot.EndedAt == 0 || snapshot.EndReason != "budget" {
		t.Fatalf("unexpected timing: %+v", snapshot)
	}
	if snapshot.TurnsCount != 1 {
		t.Errorf("turnsCount = %d, want 1", snapshot.TurnsCount)
	}
	if snapshot.InputTokens != 120 || snapshot.OutputTokens != 40 || snapshot.AudioMillis != 2500 {
		t.Errorf("usage = (%d, %d, %d), want (120, 40, 2500)", snapshot.InputTokens, snapshot.OutputTokens, snapshot.AudioMillis)
	}
	// Delegated tokens are tracked apart from the session tokens.
	if snapshot.DelegationInputTokens != 300 || snapshot.DelegationOutputTokens != 90 {
		t.Errorf("delegation usage = (%d, %d), want (300, 90)", snapshot.DelegationInputTokens, snapshot.DelegationOutputTokens)
	}
	// The sentinel is a redirect, never a tool the agent ran.
	if got := snapshot.ToolsUsed; len(got) != 1 || got["whiteboard_write_content"] != 1 {
		t.Errorf("toolsUsed = %v, want only whiteboard_write_content", got)
	}
	if snapshot.OffTopicRedirectsCount != 1 {
		t.Errorf("offTopicRedirectsCount = %d, want 1", snapshot.OffTopicRedirectsCount)
	}

	want := []redisservice.AISessionTranscriptTurn{
		{Role: "participant", Text: "what is a quarter"},
		{Role: "agent", Text: "one of four equal parts"},
	}
	if !reflect.DeepEqual(snapshot.Transcript, want) {
		t.Fatalf("transcript = %+v, want %+v", snapshot.Transcript, want)
	}

	// A still-running session reports no end time.
	if running := env.agent.TelemetrySnapshot(""); running.EndedAt != 0 || running.EndReason != "" {
		t.Errorf("a running session must leave EndedAt zero: %+v", running)
	}
}

func TestAIAgentTelemetryTranscriptKeepsTheTailAndClipsTurns(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	total := maxSessionTranscriptTurns + 5
	for i := 0; i < total; i++ {
		env.session.Push(insights.RealtimeEvent{
			Type:  insights.RealtimeEventTranscriptUser,
			Text:  fmt.Sprintf("turn-%d", i),
			Final: true,
		})
	}
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").TurnsCount == total
	}, "every participant turn counted")

	long := strings.Repeat("a", maxTranscriptTurnChars*2)
	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: long, Final: true})
	waitFor(t, func() bool {
		transcript := env.agent.TelemetrySnapshot("").Transcript
		return len(transcript) == maxSessionTranscriptTurns && transcript[len(transcript)-1].Role == "agent"
	}, "clipped agent turn appended")

	snapshot := env.agent.TelemetrySnapshot("done")
	if len(snapshot.Transcript) != maxSessionTranscriptTurns {
		t.Fatalf("transcript length = %d, want %d", len(snapshot.Transcript), maxSessionTranscriptTurns)
	}
	if snapshot.TurnsCount != total {
		t.Errorf("turnsCount = %d, want %d", snapshot.TurnsCount, total)
	}
	if first := snapshot.Transcript[0].Text; first != fmt.Sprintf("turn-%d", total-maxSessionTranscriptTurns+1) {
		t.Errorf("the oldest kept turn is %q", first)
	}
	clipped := snapshot.Transcript[len(snapshot.Transcript)-1].Text
	if !strings.HasPrefix(clipped, strings.Repeat("a", maxTranscriptTurnChars)) || len([]rune(clipped)) >= len(long) {
		t.Errorf("the agent turn was not clipped to %d runes: %q", maxTranscriptTurnChars, clipped)
	}
}

func TestAIAgentTelemetryPersistsToRedis(t *testing.T) {
	mr := miniredis.RunT(t)
	ttl := time.Hour
	store := redisservice.New(redisservice.Args{
		Ctx:    context.Background(),
		Rc:     redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		App:    &config.AppConfig{RedisInfo: connections.RedisInfo{DefaultTTL: &ttl}},
		Logger: logrus.New(),
	})

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) { args.RedisService = store })
	ctx := context.Background()

	// The opening snapshot survives a crash, so identity and topic are on record.
	initial, err := store.GetAIAgentSessionTelemetry(ctx, env.agent.RoomID())
	if err != nil || initial == nil {
		t.Fatalf("the opening snapshot was not saved (%+v, err %v)", initial, err)
	}
	if initial.SessionID != "session-1" || initial.Topic != "Grade 4 Fractions" || initial.EndedAt != 0 {
		t.Fatalf("unexpected opening snapshot: %+v", initial)
	}

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "hello there", Final: true})
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").TurnsCount == 1
	}, "participant turn counted")

	// Stop cancels the session context first; the flush must still land.
	env.agent.Stop("budget")

	saved, err := store.GetAIAgentSessionTelemetry(ctx, env.agent.RoomID())
	if err != nil || saved == nil {
		t.Fatalf("the closing snapshot was not saved (%+v, err %v)", saved, err)
	}
	if saved.EndReason != "budget" || saved.EndedAt == 0 || saved.TurnsCount != 1 {
		t.Fatalf("unexpected closing snapshot: %+v", saved)
	}
	if len(saved.Transcript) != 1 || saved.Transcript[0].Text != "hello there" {
		t.Fatalf("unexpected transcript: %+v", saved.Transcript)
	}
}

// TestAIAgentSetLearnerPersistsImmediately locks in that pairing a participant
// flushes the telemetry at once, so Redis knows the learner long before the
// session stops.
func TestAIAgentSetLearnerPersistsImmediately(t *testing.T) {
	mr := miniredis.RunT(t)
	ttl := time.Hour
	store := redisservice.New(redisservice.Args{
		Ctx:    context.Background(),
		Rc:     redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		App:    &config.AppConfig{RedisInfo: connections.RedisInfo{DefaultTTL: &ttl}},
		Logger: logrus.New(),
	})

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) { args.RedisService = store })
	ctx := context.Background()

	initial, err := store.GetAIAgentSessionTelemetry(ctx, env.agent.RoomID())
	if err != nil || initial == nil || initial.UserID != "" {
		t.Fatalf("unexpected opening snapshot (%+v, err %v)", initial, err)
	}

	env.agent.SetLearner(LearnerIdentity{UserID: "learner-1", UserName: "Learner One", ExUserID: "external-1"})

	// No Stop: the pairing alone must be enough for Redis to know the learner.
	saved, err := store.GetAIAgentSessionTelemetry(ctx, env.agent.RoomID())
	if err != nil || saved == nil {
		t.Fatalf("the learner was not flushed (%+v, err %v)", saved, err)
	}
	if saved.UserID != "learner-1" || saved.UserName != "Learner One" || saved.ExUserID != "external-1" {
		t.Fatalf("saved learner = %+v, want the paired participant", saved)
	}
	if saved.EndedAt != 0 {
		t.Errorf("endedAt = %d, want a still running session", saved.EndedAt)
	}
}

func TestAIAgentStatusReportsRuntimeState(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	status := env.agent.Status()
	if !status.GetStatus() {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.GetState().GetSessionId() != "session-1" || status.GetState().GetAgentUserId() != "pnm_agent-ai_agent-test" {
		t.Fatalf("unexpected state: %+v", status.GetState())
	}
}

// TestAIAgentTelemetryLearner locks in that the learner identity given at start
// reaches the telemetry, and that a later partial lookup only fills the gaps.
func TestAIAgentTelemetryLearner(t *testing.T) {
	env := newAgentTestEnv(t, mcp.NewRegistry(), func(args *aiAgentCoreArgs) {
		args.Learner = LearnerIdentity{UserID: "learner-1", UserName: "Learner One"}
	})

	snapshot := env.agent.TelemetrySnapshot("")
	if snapshot.UserID != "learner-1" || snapshot.UserName != "Learner One" {
		t.Fatalf("learner = %q/%q, want the identity given at start", snapshot.UserID, snapshot.UserName)
	}

	// Only missing details of the same participant are accepted.
	env.agent.SetLearner(LearnerIdentity{UserID: "learner-1", ExUserID: "external-1"})
	snapshot = env.agent.TelemetrySnapshot("")
	if snapshot.UserID != "learner-1" || snapshot.UserName != "Learner One" {
		t.Fatalf("learner = %q/%q, want the known identity kept", snapshot.UserID, snapshot.UserName)
	}
	if snapshot.ExUserID != "external-1" {
		t.Fatalf("external id = %q, want the later lookup to fill it", snapshot.ExUserID)
	}

	// A supervisor joining to observe must not become the learner.
	env.agent.SetLearner(LearnerIdentity{UserID: "supervisor-1", UserName: "Supervisor"})
	snapshot = env.agent.TelemetrySnapshot("")
	if snapshot.UserID != "learner-1" || snapshot.UserName != "Learner One" {
		t.Fatalf("learner = %q/%q, want the pinned learner", snapshot.UserID, snapshot.UserName)
	}
}

// TestAIAgentRollingSummaryRunsInBackground locks in that the conversation is
// summarized in the background once the window fills, and that the summary and
// its tokens are carried by the telemetry.
func TestAIAgentRollingSummaryRunsInBackground(t *testing.T) {
	var mu sync.Mutex
	var histories [][]*plugnmeet.InsightsAITextChatContent

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.SummarizeWindow = 2
		args.Summarize = func(_ context.Context, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
			mu.Lock()
			histories = append(histories, history)
			mu.Unlock()
			return "the learner practised halves", 30, 10, nil
		}
	})

	env.session.Push(
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "what is a half", Final: true},
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: "one of two equal parts", Final: true},
	)
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").RollingSummary != ""
	}, "rolling summary stored")

	snapshot := env.agent.TelemetrySnapshot("")
	if snapshot.RollingSummary != "the learner practised halves" {
		t.Errorf("rolling summary = %q", snapshot.RollingSummary)
	}
	if snapshot.SummarizedTurns != 2 || snapshot.TotalTranscriptTurns != 2 {
		t.Errorf("turns = %d/%d, want (2, 2)", snapshot.SummarizedTurns, snapshot.TotalTranscriptTurns)
	}
	if snapshot.SummaryInputTokens != 30 || snapshot.SummaryOutputTokens != 10 {
		t.Errorf("summary tokens = (%d, %d), want (30, 10)", snapshot.SummaryInputTokens, snapshot.SummaryOutputTokens)
	}

	mu.Lock()
	first := histories[0]
	mu.Unlock()
	if len(first) != 2 {
		t.Fatalf("history = %d entries, want the two turns", len(first))
	}
	if first[0].GetRole() != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER ||
		first[1].GetRole() != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL {
		t.Errorf("roles = %v/%v, want USER/MODEL", first[0].GetRole(), first[1].GetRole())
	}

	// The next window folds into the previous summary instead of starting over.
	env.session.Push(
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "what is a quarter", Final: true},
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: "one of four equal parts", Final: true},
	)
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").SummarizedTurns == 4
	}, "the second window summarized")

	mu.Lock()
	last := histories[len(histories)-1]
	mu.Unlock()
	if last[0].GetRole() != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM ||
		!strings.Contains(last[0].GetText(), "the learner practised halves") {
		t.Fatalf("history = %v, want the running summary first", last[0])
	}
	if snapshot := env.agent.TelemetrySnapshot(""); snapshot.SummaryInputTokens != 60 || snapshot.SummaryOutputTokens != 20 {
		t.Errorf("summary tokens = (%d, %d), want the cumulative (60, 20)",
			snapshot.SummaryInputTokens, snapshot.SummaryOutputTokens)
	}
}

// TestAIAgentRedialInjectsReconnectHandoff locks in that a provider reconnect
// hands the new socket the conversation so far, so the agent does not start over.
func TestAIAgentRedialInjectsReconnectHandoff(t *testing.T) {
	next := fake.NewSession()
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.NewSession = func(context.Context) (insights.RealtimeSession, error) { return next, nil }
	})

	env.session.Push(
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptUser, Text: "what is a quarter", Final: true},
		insights.RealtimeEvent{Type: insights.RealtimeEventTranscriptAgent, Text: "one of four equal parts", Final: true},
	)
	waitFor(t, func() bool {
		return env.agent.TelemetrySnapshot("").TotalTranscriptTurns == 2
	}, "turns recorded")

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventGoAway})
	waitFor(t, func() bool {
		return env.agent.currentSession() == next
	}, "session replaced")

	waitFor(t, func() bool {
		for _, injected := range next.Contexts() {
			if len(injected.Text) > 0 && strings.Contains(injected.Text, reconnectHandoffHeader) &&
				strings.Contains(injected.Text, "participant: what is a quarter") &&
				strings.Contains(injected.Text, "agent: one of four equal parts") {
				return true
			}
		}
		return false
	}, "reconnect handoff injected")
}

// TestAIAgentResumesPreviousSession locks in that a restart in the same room
// carries the earlier usage and conversation forward instead of wiping them.
func TestAIAgentResumesPreviousSession(t *testing.T) {
	previous := &redisservice.AIAgentSessionTelemetry{
		SessionID:            "session-0",
		TurnsCount:           3,
		InputTokens:          1200,
		OutputTokens:         400,
		CachedInputTokens:    300,
		AudioMillis:          45000,
		ToolsUsed:            map[string]int{"whiteboard_write_content": 2},
		Transcript:           []redisservice.AISessionTranscriptTurn{{Role: "participant", Text: "earlier question"}, {Role: "agent", Text: "earlier answer"}},
		RollingSummary:       "the session covered halves",
		SummarizedTurns:      2,
		TotalTranscriptTurns: 6,
		SummaryInputTokens:   50,
		SummaryOutputTokens:  20,
	}

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) { args.PreviousTelemetry = previous })

	snapshot := env.agent.TelemetrySnapshot("")
	if snapshot.TurnsCount != 3 || snapshot.InputTokens != 1200 || snapshot.OutputTokens != 400 ||
		snapshot.CachedInputTokens != 300 || snapshot.AudioMillis != 45000 {
		t.Errorf("usage = %+v, want the earlier session's counters", snapshot)
	}
	if snapshot.SummaryInputTokens != 50 || snapshot.SummaryOutputTokens != 20 {
		t.Errorf("summary tokens = (%d, %d), want (50, 20)", snapshot.SummaryInputTokens, snapshot.SummaryOutputTokens)
	}
	if snapshot.RollingSummary != "the session covered halves" || snapshot.SummarizedTurns != 2 ||
		snapshot.TotalTranscriptTurns != 6 {
		t.Errorf("summary state = %+v, want the earlier session's", snapshot)
	}
	if len(snapshot.Transcript) != 2 || snapshot.ToolsUsed["whiteboard_write_content"] != 2 {
		t.Errorf("transcript/tools = %+v/%+v, want the earlier session's", snapshot.Transcript, snapshot.ToolsUsed)
	}

	waitFor(t, func() bool {
		for _, injected := range env.session.Contexts() {
			if len(injected.Text) > 0 && strings.Contains(injected.Text, reconnectHandoffHeader) &&
				strings.Contains(injected.Text, "the session covered halves") &&
				strings.Contains(injected.Text, "agent: earlier answer") {
				return true
			}
		}
		return false
	}, "reconnect handoff injected on start")
}

// A turn boundary is where one agent utterance ends, so the publisher has to be
// told: a datastream client can only tell two utterances apart by that.
func TestAIAgentEndsAudioSegmentAtTurnComplete(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})

	waitFor(t, func() bool {
		return env.publisher.segmentCount() == 1
	}, "the audio segment ended on turn complete")
}

// A tool call can arrive while the sentence announcing it is still being heard,
// so only a completed turn closes the audio segment.
func TestAIAgentKeepsAudioSegmentOpenDuringToolCall(t *testing.T) {
	registry := mcp.NewRegistry()
	registry.Register(echoTool("whiteboard_write_content"))
	env := newAgentTestEnv(t, registry, nil)

	env.session.Push(insights.RealtimeEvent{
		Type:        insights.RealtimeEventAudioDelta,
		AudioPCM24k: []byte{1, 2, 3, 4},
	})
	env.session.Push(insights.RealtimeEvent{
		Type:     insights.RealtimeEventToolCall,
		CallID:   "call-segment",
		ToolName: "whiteboard_write_content",
		ArgsJSON: `{}`,
	})

	waitFor(t, func() bool {
		return len(env.session.ToolResults()) == 1
	}, "tool result sent")
	if got := env.publisher.segmentCount(); got != 0 {
		t.Fatalf("segment ends = %d, want the segment to stay open across the tool call", got)
	}

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventTurnComplete})
	waitFor(t, func() bool {
		return env.publisher.segmentCount() == 1
	}, "the completed turn closed the segment")
}

// The avatar provider reaches the browser, so the session pill can show which
// avatar is talking.
func TestAIAgentReportsAvatarProvider(t *testing.T) {
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.Avatar = &AIAgentAvatarConfig{
			Provider: config.ProviderAnam,
			AvatarID: "avatar-1",
		}
	})

	if got := env.agent.Status().GetState().GetAvatarProvider(); got != string(config.ProviderAnam) {
		t.Fatalf("status avatar provider = %q, want %q", got, config.ProviderAnam)
	}
	waitFor(t, func() bool {
		return env.transport.lastStateMsg().GetAvatarProvider() == string(config.ProviderAnam)
	}, "the avatar provider was broadcast")

	// without an avatar the optional field stays absent
	plain := newAgentTestEnv(t, nil, nil)
	if got := plain.agent.Status().GetState().GetAvatarProvider(); got != "" {
		t.Fatalf("status avatar provider = %q, want it empty without an avatar", got)
	}
	if plain.agent.Status().GetState().AvatarProvider != nil {
		t.Fatal("the avatar provider must stay unset without an avatar")
	}
}

// The avatar budget is spent across restarts of the same room, and the duration
// it burned reaches the telemetry with the provider that served it.
func TestAIAgentAvatarBudgetStopsSession(t *testing.T) {
	mr := miniredis.RunT(t)
	ttl := time.Hour
	store := redisservice.New(redisservice.Args{
		Ctx:    context.Background(),
		Rc:     redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		App:    &config.AppConfig{RedisInfo: connections.RedisInfo{DefaultTTL: &ttl}},
		Logger: logrus.New(),
	})

	// The earlier session already spent almost the whole minute, so this one has
	// a couple of hundred milliseconds left.
	previous := &redisservice.AIAgentSessionTelemetry{SessionID: "session-0", AvatarMillis: 59800}

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.RedisService = store
		args.PreviousTelemetry = previous
		args.Avatar = &AIAgentAvatarConfig{
			Provider:   config.ProviderBeyondPresence,
			AvatarID:   "avatar-1",
			IsCustom:   true,
			MaxMinutes: 1,
		}
	})

	var saved *redisservice.AIAgentSessionTelemetry
	waitFor(t, func() bool {
		telemetry, err := store.GetAIAgentSessionTelemetry(context.Background(), env.agent.RoomID())
		if err != nil || telemetry == nil {
			return false
		}
		saved = telemetry
		return telemetry.EndReason == "budget"
	}, "the avatar budget stopped the session")

	if saved.AvatarMillis < 59800 {
		t.Errorf("avatar_millis = %d, want the earlier session's duration continued", saved.AvatarMillis)
	}
	if saved.AvatarProvider != string(config.ProviderBeyondPresence) || saved.CustomAvatarProvider != string(config.ProviderBeyondPresence) {
		t.Errorf("avatar providers = (%q, %q), want the custom beyond_presence avatar",
			saved.AvatarProvider, saved.CustomAvatarProvider)
	}
}

// The avatar twin is the participant the provider joins the room as, so its token
// must name the agent in both places the proxy and the webhooks look: the
// plugNmeet attribute and LiveKit's publish-on-behalf attribute.
func TestMintAvatarLiveKitAccessTokenCarriesTheOriginalUserId(t *testing.T) {
	const (
		apiKey       = "test-api-key"
		secret       = "test-secret-that-is-long-enough-for-hmac-sha256"
		roomId       = "room-01"
		twinIdentity = "pnm_agent-ai_agent-01-native"
		displayName  = "Arjun"
		agentUserId  = "pnm_agent-ai_agent-01"
	)

	// go-jose refuses HMAC keys shorter than 32 bytes, and a real LiveKit
	// secret is much longer than that anyway.
	token, err := mintAvatarLiveKitAccessToken(apiKey, secret, time.Hour, roomId, twinIdentity, displayName, agentUserId)
	if err != nil {
		t.Fatalf("mint the avatar token: %v", err)
	}

	cl, grants := decodeLiveKitTokenClaims(t, token, secret)

	if err := cl.Validate(jwt.Expected{Issuer: apiKey, Subject: twinIdentity, Time: time.Now().UTC()}); err != nil {
		t.Fatalf("the avatar token is not valid: %v", err)
	}
	if cl.Expiry == nil || time.Until(cl.Expiry.Time()) < 30*time.Minute {
		t.Errorf("expiry = %v, want the requested hour", cl.Expiry)
	}

	if grants.Identity != twinIdentity {
		t.Errorf("identity = %q, want %q", grants.Identity, twinIdentity)
	}
	if grants.Name != displayName {
		t.Errorf("name = %q, want %q", grants.Name, displayName)
	}
	if grants.Video == nil {
		t.Fatal("the token carries no video grant")
	}
	if grants.Video.Room != roomId || !grants.Video.RoomJoin {
		t.Errorf("video grant = %+v, want room join for %q", grants.Video, roomId)
	}
	if grants.Video.CanPublish == nil || !*grants.Video.CanPublish {
		t.Error("the twin must be allowed to publish")
	}
	if grants.Video.CanPublishData == nil || !*grants.Video.CanPublishData {
		t.Error("the twin must be allowed to publish its data stream")
	}
	// The twin only plays the voice of the agent; room administration is not its job.
	if grants.Video.RoomAdmin {
		t.Error("the avatar twin must not be a room admin")
	}
	for _, key := range []string{"original_user_id", "lk.publish_on_behalf"} {
		if got := grants.Attributes[key]; got != agentUserId {
			t.Errorf("attribute %q = %q, want %q", key, got, agentUserId)
		}
	}
	// livekit-plugins-bey sets .with_kind("agent") on the avatar token; a
	// STANDARD twin would not be recognised as an agent participant.
	if got := grants.GetParticipantKind(); got != livekit.ParticipantInfo_AGENT {
		t.Errorf("participant kind = %v, want %v", got, livekit.ParticipantInfo_AGENT)
	}
}

// The RoomAgent itself has to join with kind AGENT: agent SDK workers (such as
// livekit-agents' DataStreamAudioReceiver) wait for a remote participant of
// that kind and time out when only a STANDARD participant joins.
func TestMintRoomAgentLiveKitAccessTokenJoinsAsAgent(t *testing.T) {
	const (
		apiKey        = "test-api-key"
		secret        = "test-secret-that-is-long-enough-for-hmac-sha256"
		roomId        = "room-01"
		agentUserId   = "pnm_agent-ai_agent-01"
		agentName     = "Arjun"
		tokenValidity = time.Hour
	)

	token, err := mintRoomAgentLiveKitAccessToken(apiKey, secret, tokenValidity, &plugnmeet.PlugNmeetTokenClaims{
		RoomId:   roomId,
		UserId:   agentUserId,
		Name:     agentName,
		IsAdmin:  true,
		IsHidden: true,
	})
	if err != nil {
		t.Fatalf("mint the room agent token: %v", err)
	}

	cl, grants := decodeLiveKitTokenClaims(t, token, secret)

	if err := cl.Validate(jwt.Expected{Issuer: apiKey, Subject: agentUserId, Time: time.Now().UTC()}); err != nil {
		t.Fatalf("the room agent token is not valid: %v", err)
	}
	if cl.Expiry == nil || time.Until(cl.Expiry.Time()) < 30*time.Minute {
		t.Errorf("expiry = %v, want the requested hour", cl.Expiry)
	}
	if got := grants.GetParticipantKind(); got != livekit.ParticipantInfo_AGENT {
		t.Errorf("participant kind = %v, want %v", got, livekit.ParticipantInfo_AGENT)
	}
	if grants.Identity != agentUserId {
		t.Errorf("identity = %q, want %q", grants.Identity, agentUserId)
	}
	if grants.Name != agentName {
		t.Errorf("name = %q, want %q", grants.Name, agentName)
	}
	if grants.Video == nil {
		t.Fatal("the token carries no video grant")
	}
	if grants.Video.Room != roomId || !grants.Video.RoomJoin {
		t.Errorf("video grant = %+v, want room join for %q", grants.Video, roomId)
	}
	if !grants.Video.RoomAdmin {
		t.Error("the room agent must be a room admin")
	}
	if !grants.Video.Hidden {
		t.Error("the room agent must stay hidden")
	}
	if grants.Video.CanPublish == nil || !*grants.Video.CanPublish {
		t.Error("the room agent must be allowed to publish")
	}
	if grants.Video.CanSubscribe == nil || !*grants.Video.CanSubscribe {
		t.Error("the room agent must be allowed to subscribe")
	}
}

// decodeLiveKitTokenClaims verifies a token signature with the LiveKit secret
// and returns both the standard claims and the LiveKit grant claims.
func decodeLiveKitTokenClaims(t *testing.T, token, secret string) (*jwt.Claims, *lkauth.ClaimGrants) {
	t.Helper()

	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		t.Fatalf("parse the LiveKit token: %v", err)
	}

	var cl jwt.Claims
	var grants lkauth.ClaimGrants
	// passing the secret as the key verifies the signature as it decodes
	if err := tok.Claims([]byte(secret), &cl, &grants); err != nil {
		t.Fatalf("verify the LiveKit token: %v", err)
	}

	return &cl, &grants
}
