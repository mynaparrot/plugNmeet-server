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
	"github.com/livekit/media-sdk"
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
	mu      sync.Mutex
	written []byte
	flushes int
	closed  bool
}

func (p *recordingPublisher) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, data...)
	return len(data), nil
}

func (p *recordingPublisher) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushes++
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

type recordingTransport struct {
	mu     sync.Mutex
	states []*plugnmeet.AIAgentStateMsg
	calls  []*plugnmeet.AIAgentToolCallMsg
}

func (t *recordingTransport) BroadcastAIAgentState(_ string, msg *plugnmeet.AIAgentStateMsg) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.states = append(t.states, msg)
	return nil
}

func (t *recordingTransport) SendAIAgentToolCallToUser(_, _ string, msg *plugnmeet.AIAgentToolCallMsg) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, msg)
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

func (t *recordingTransport) toolCalls() []*plugnmeet.AIAgentToolCallMsg {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*plugnmeet.AIAgentToolCallMsg(nil), t.calls...)
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
		Delegate:    func() (string, error) { return "user-1", nil },
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
	waitFor(t, func() bool {
		return env.transport.lastState() == plugnmeet.AIAgentRuntimeState_AI_AGENT_RUNTIME_STATE_LISTENING
	}, "state restored after tool")
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
}

func TestAIAgentClientToolRPC(t *testing.T) {
	env := newAgentTestEnv(t, nil, nil)

	go func() {
		// The delegate answers the call from its browser.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if calls := env.transport.toolCalls(); len(calls) == 1 {
				if calls[0].GetTargetUserId() != "user-1" {
					t.Errorf("unexpected target: %s", calls[0].GetTargetUserId())
				}
				env.agent.HandleClientToolResult(&plugnmeet.AIAgentToolResultMsg{
					CallId:     calls[0].GetCallId(),
					Ok:         true,
					ResultJson: `{"ids":["a"]}`,
				})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	result, err := env.agent.CallClientTool(context.Background(), mcp.ToolCall{
		CallID:    "call-3",
		RoomID:    "room",
		ToolName:  "whiteboard_draw_shapes",
		ArgsJSON:  `{}`,
		SessionID: "session-1",
	}, 2*time.Second)
	if err != nil {
		t.Fatalf("client tool call: %v", err)
	}
	if result.OutputJSON != `{"ids":["a"]}` {
		t.Fatalf("unexpected result: %s", result.OutputJSON)
	}
}

func TestAIAgentClientToolWithoutDelegateFails(t *testing.T) {
	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.Delegate = func() (string, error) { return "", nil }
	})

	if _, err := env.agent.CallClientTool(context.Background(), mcp.ToolCall{CallID: "call-4", ToolName: "notepad_write"}, time.Second); err == nil {
		t.Fatal("expected an error when no delegate is available")
	}
	if calls := env.transport.toolCalls(); len(calls) != 0 {
		t.Fatalf("no tool call should be published, got %d", len(calls))
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
