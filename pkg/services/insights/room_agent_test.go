package insightsservice

import (
	"strings"
	"sync"
	"testing"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// The AI agent registers its identity in NATS and then joins LiveKit with that
// same id, because browsers resolve the agent's audio track through the
// NATS-backed participant list.
func TestRoomAgentIdentityReusesProvidedAgentUserId(t *testing.T) {
	payload := &insights.InsightsTaskPayload{ServiceType: insights.ServiceTypeAIAgent}
	const agentUserId = "pnm_agent-ai_agent-fixed"

	if got := roomAgentIdentity(payload, agentUserId); got != agentUserId {
		t.Fatalf("expected the provided identity %q, got %q", agentUserId, got)
	}

	generated := roomAgentIdentity(payload, "")
	if !strings.HasPrefix(generated, config.AgentUserUserIdPrefix+string(insights.ServiceTypeAIAgent)+"-") {
		t.Fatalf("unexpected generated identity %q", generated)
	}
	if again := roomAgentIdentity(payload, ""); again == generated {
		t.Fatalf("expected a fresh identity per call, got %q twice", generated)
	}
}

func newTestPipeline(trackID string) (*activePipeline, chan struct{}) {
	canceled := make(chan struct{})
	var once sync.Once

	return &activePipeline{
		trackID: trackID,
		cancel:  func() { once.Do(func() { close(canceled) }) },
	}, canceled
}

func newTestAgent(pipelines map[string]*activePipeline) *RoomAgent {
	return &RoomAgent{
		logger:          testLogger(),
		activePipelines: pipelines,
		activeUserTasks: map[string][]byte{},
		payload:         &insights.InsightsTaskPayload{ServiceType: insights.ServiceTypeTranscription},
	}
}

// newTestParticipant returns a distinct participant object. The LiveKit SDK
// offers no way to set identity or SID, so all test participants share the
// empty identity and only their pointer differs.
func newTestParticipant() *lksdk.RemoteParticipant {
	return &lksdk.RemoteParticipant{}
}

// sdkParticipant creates a participant through the SDK's public update path,
// the only way to get a participant with a real SID. Reusing the identity
// updates the existing object, which is how the SDK applies an in-place SID
// change.
func sdkParticipant(t *testing.T, room *lksdk.Room, identity, sid string) *lksdk.RemoteParticipant {
	t.Helper()

	room.OnParticipantUpdate([]*livekit.ParticipantInfo{{Identity: identity, Sid: sid}})
	rp := room.GetParticipantByIdentity(identity)
	if rp == nil || rp.SID() != sid {
		t.Fatalf("could not create participant %q with sid %q", identity, sid)
	}

	return rp
}

// pipelineKey mirrors the map key the agent derives for a participant.
func pipelineKey(rp *lksdk.RemoteParticipant) string {
	return config.PrimaryIdentityFromNative(rp.Identity())
}

// newParticipantPipeline builds a pipeline owned by the given participant.
func newParticipantPipeline(rp *lksdk.RemoteParticipant, trackID string) (*activePipeline, chan struct{}) {
	pipeline, canceled := newTestPipeline(trackID)
	pipeline.participant = rp
	pipeline.participantSID = rp.SID()

	return pipeline, canceled
}

func isCanceled(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// A late failure report must not touch a replacement pipeline that reuses the
// same track SID.
func TestCleanupFailedPipelineMatchesPointer(t *testing.T) {
	failed, failedCanceled := newTestPipeline("TR_same")
	replacement, replacementCanceled := newTestPipeline("TR_same")
	agent := newTestAgent(map[string]*activePipeline{"user-1": replacement})
	agent.activeUserTasks["user-1"] = []byte("{}")

	agent.cleanupFailedPipeline("user-1", failed)

	if got := agent.activePipelines["user-1"]; got != replacement {
		t.Fatal("a stale failure must not remove the replacement pipeline")
	}
	if _, ok := agent.activeUserTasks["user-1"]; !ok {
		t.Fatal("a stale failure must not stop the user's active task")
	}
	if isCanceled(replacementCanceled) {
		t.Fatal("a stale failure must not cancel the replacement pipeline")
	}
	if isCanceled(failedCanceled) {
		t.Fatal("the stale pipeline does not own the participant and must not be canceled here")
	}
}

// The pipeline that reported the failure is released together with the user's
// activation state.
func TestCleanupFailedPipelineRemovesOwnedPipeline(t *testing.T) {
	failed, failedCanceled := newTestPipeline("TR_1")
	agent := newTestAgent(map[string]*activePipeline{"user-1": failed})
	agent.activeUserTasks["user-1"] = []byte("{}")

	agent.cleanupFailedPipeline("user-1", failed)

	if _, ok := agent.activePipelines["user-1"]; ok {
		t.Fatal("the failed pipeline must be removed")
	}
	if _, ok := agent.activeUserTasks["user-1"]; ok {
		t.Fatal("the failed pipeline must end the user's activation")
	}
	if !isCanceled(failedCanceled) {
		t.Fatal("the failed pipeline must be canceled")
	}
}

type fakeSubscription struct {
	mu       sync.Mutex
	unsubbed int
}

func (f *fakeSubscription) SetSubscribed(subscribed bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !subscribed {
		f.unsubbed++
	}
	return nil
}

func (f *fakeSubscription) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unsubbed
}

// A reused track must not be unsubscribed while a replacement pipeline owns it.
func TestUnsubscribePipelineTrackSkipsReplacement(t *testing.T) {
	sub := &fakeSubscription{}
	old := &activePipeline{publication: sub}
	replacement, _ := newTestPipeline("TR_same")
	agent := newTestAgent(map[string]*activePipeline{"user-1": replacement})

	agent.unsubscribePipelineTrack("user-1", old)

	if got := sub.count(); got != 0 {
		t.Fatalf("replacement track was unsubscribed %d time(s)", got)
	}
}

// An activation that recorded its intent but has no pipeline yet owns the track.
func TestUnsubscribePipelineTrackWaitsForActivation(t *testing.T) {
	sub := &fakeSubscription{}
	old := &activePipeline{publication: sub}
	agent := newTestAgent(map[string]*activePipeline{})
	agent.activeUserTasks["user-1"] = []byte("{}")

	agent.unsubscribePipelineTrack("user-1", old)

	if got := sub.count(); got != 0 {
		t.Fatalf("in-flight activation's track was unsubscribed %d time(s)", got)
	}
}

// Once the participant has no pipeline or activation, the old track is released.
func TestUnsubscribePipelineTrackReleasesOldTrack(t *testing.T) {
	sub := &fakeSubscription{}
	old := &activePipeline{publication: sub}
	agent := newTestAgent(map[string]*activePipeline{})

	agent.unsubscribePipelineTrack("user-1", old)

	if got := sub.count(); got != 1 {
		t.Fatalf("expected exactly one unsubscribe, got %d", got)
	}
}

// A pipeline belongs to the participant object it was created for.
func TestMatchesParticipant(t *testing.T) {
	rp := newTestParticipant()

	cases := map[string]struct {
		pipeline *activePipeline
		rp       *lksdk.RemoteParticipant
		want     bool
	}{
		"same object":      {&activePipeline{participant: rp}, rp, true},
		"unbound pipeline": {&activePipeline{}, rp, true},
		"replaced object":  {&activePipeline{participant: newTestParticipant()}, rp, false},
		"missing pipeline": {nil, rp, false},
		"nil participant":  {&activePipeline{participant: rp}, nil, false},
	}

	for name, c := range cases {
		if got := c.pipeline.matchesParticipant(c.rp); got != c.want {
			t.Fatalf("%s: expected %t, got %t", name, c.want, got)
		}
	}
}

// An unknown SID is not a mismatch: the SDK leaves it empty until the session
// is established.
func TestMatchesSID(t *testing.T) {
	cases := map[string]struct {
		recorded string
		current  string
		want     bool
	}{
		"same session":     {"PA_1", "PA_1", true},
		"changed sid":      {"PA_1", "PA_2", false},
		"unknown recorded": {"", "PA_2", true},
		"unknown current":  {"PA_1", "", true},
		"both unknown":     {"", "", true},
	}

	for name, c := range cases {
		if got := matchesSID(c.recorded, c.current); got != c.want {
			t.Fatalf("%s: expected %t, got %t", name, c.want, got)
		}
	}
}

// A subscription that captured its session before the unlocked transcoder
// build must be dropped when a newer session installed its pipeline meanwhile.
// The SDK usually mutates the same participant object in place, so the SID
// captured at entry decides instead of rp.SID(), which already points at the
// newer session.
func TestPipelineStaleFor(t *testing.T) {
	room := lksdk.NewRoom(lksdk.NewRoomCallback())
	rp := sdkParticipant(t, room, "user-1", "PA_1")
	// A reconnect keeps the object and only swaps its SID.
	sdkParticipant(t, room, "user-1", "PA_2")
	replaced := newTestParticipant()

	cases := map[string]struct {
		pipeline *activePipeline
		captured string
		want     bool
	}{
		"newer session swapped the sid in place": {&activePipeline{participant: rp, participantSID: "PA_2"}, "PA_1", true},
		"same session renegotiated its track":    {&activePipeline{participant: rp, participantSID: "PA_2"}, "PA_2", false},
		"newer session replaced the object":      {&activePipeline{participant: replaced, participantSID: "PA_2"}, "PA_2", true},
		"unknown sid on both sides":              {&activePipeline{participant: rp}, "", false},
		"unbound pipeline":                       {&activePipeline{}, "PA_2", false},
		"missing pipeline":                       {nil, "PA_2", false},
	}

	for name, c := range cases {
		if got := c.pipeline.staleFor(rp, c.captured); got != c.want {
			t.Fatalf("%s: expected %t, got %t", name, c.want, got)
		}
	}
}

// The SDK can swap only the SID of an existing participant object.
func TestStopStalePipelineStopsInPlaceSIDChange(t *testing.T) {
	room := lksdk.NewRoom(lksdk.NewRoomCallback())
	rp := sdkParticipant(t, room, "user-1", "PA_1")
	pipeline, canceled := newParticipantPipeline(rp, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{pipelineKey(rp): pipeline})

	sdkParticipant(t, room, "user-1", "PA_2")
	agent.stopStalePipeline(rp)

	if _, ok := agent.activePipelines[pipelineKey(rp)]; ok {
		t.Fatal("the pipeline of the previous SID must be removed")
	}
	if !isCanceled(canceled) {
		t.Fatal("the pipeline of the previous SID must be canceled")
	}
}

// A disconnect stops the pipeline even when only the SID changed, because the
// audio of the old session is gone either way.
func TestParticipantDisconnectStopsPipelineAfterSIDChange(t *testing.T) {
	room := lksdk.NewRoom(lksdk.NewRoomCallback())
	rp := sdkParticipant(t, room, "user-1", "PA_1")
	pipeline, canceled := newParticipantPipeline(rp, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{pipelineKey(rp): pipeline})

	sdkParticipant(t, room, "user-1", "PA_2")
	agent.onParticipantDisconnected(rp)

	if _, ok := agent.activePipelines[pipelineKey(rp)]; ok {
		t.Fatal("the pipeline must be removed on disconnect")
	}
	if !isCanceled(canceled) {
		t.Fatal("the pipeline must be canceled on disconnect")
	}
}

// A reconnect hands out a new participant object, so the pipeline of the
// previous session can never receive audio again.
func TestStopStalePipelineStopsReplacedParticipant(t *testing.T) {
	rp := newTestParticipant()
	stale, canceled := newParticipantPipeline(newTestParticipant(), "TR_1")
	agent := newTestAgent(map[string]*activePipeline{pipelineKey(rp): stale})

	agent.stopStalePipeline(rp)

	if _, ok := agent.activePipelines[pipelineKey(rp)]; ok {
		t.Fatal("the pipeline of the replaced participant must be removed")
	}
	if !isCanceled(canceled) {
		t.Fatal("the pipeline of the replaced participant must be canceled")
	}
}

// The pipeline of the current participant must survive every participant event.
func TestStopStalePipelineKeepsCurrentPipeline(t *testing.T) {
	rp := newTestParticipant()
	current, canceled := newParticipantPipeline(rp, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{pipelineKey(rp): current})

	agent.stopStalePipeline(rp)

	if agent.activePipelines[pipelineKey(rp)] != current {
		t.Fatal("the pipeline of the current participant must stay registered")
	}
	if isCanceled(canceled) {
		t.Fatal("the pipeline of the current participant must not be canceled")
	}
}

// A disconnect stops the participant's pipeline but keeps the task activation,
// so a reconnect resumes transcription without user interaction.
func TestParticipantDisconnectStopsOwnedPipeline(t *testing.T) {
	rp := newTestParticipant()
	key := pipelineKey(rp)
	pipeline, canceled := newParticipantPipeline(rp, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{key: pipeline})
	agent.activeUserTasks[key] = []byte("{}")

	agent.onParticipantDisconnected(rp)

	if _, ok := agent.activePipelines[key]; ok {
		t.Fatal("the pipeline of the disconnected participant must be removed")
	}
	if !isCanceled(canceled) {
		t.Fatal("the pipeline of the disconnected participant must be canceled")
	}
	if _, ok := agent.activeUserTasks[key]; !ok {
		t.Fatal("the task activation must survive a disconnect")
	}
}

// The SDK delivers onParticipantDisconnected asynchronously, so it can arrive
// after the participant reconnected: the replacement must survive untouched.
func TestParticipantDisconnectKeepsReplacementPipeline(t *testing.T) {
	reconnected := newTestParticipant()
	key := pipelineKey(reconnected)
	replacement, canceled := newParticipantPipeline(reconnected, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{key: replacement})

	agent.onParticipantDisconnected(newTestParticipant())

	if agent.activePipelines[key] != replacement {
		t.Fatal("the replacement pipeline must stay registered")
	}
	if isCanceled(canceled) {
		t.Fatal("the replacement pipeline must not be canceled")
	}
}

// An unsubscribe fired by a replaced participant or for another track must
// never cancel the pipeline that is currently running.
func TestStopPipelineForTrackIgnoresStaleEvents(t *testing.T) {
	rp := newTestParticipant()
	key := pipelineKey(rp)

	current, canceled := newParticipantPipeline(rp, "TR_1")
	agent := newTestAgent(map[string]*activePipeline{key: current})

	for name, tc := range map[string]struct {
		rp      *lksdk.RemoteParticipant
		trackID string
	}{
		"replaced participant": {newTestParticipant(), "TR_1"},
		"another track":        {rp, "TR_other"},
	} {
		agent.stopPipelineForTrack(tc.rp, tc.trackID)
		if agent.activePipelines[key] != current {
			t.Fatalf("the pipeline must survive an unsubscribe of a %s", name)
		}
		if isCanceled(canceled) {
			t.Fatalf("the pipeline must not be canceled by an unsubscribe of a %s", name)
		}
	}

	agent.stopPipelineForTrack(rp, "TR_1")
	if _, ok := agent.activePipelines[key]; ok {
		t.Fatal("the owning participant's pipeline must be removed")
	}
	if !isCanceled(canceled) {
		t.Fatal("the owning participant's pipeline must be canceled")
	}
}

// Replacement racing with the helper must never leave the agent with a live
// intent but a missing replacement pipeline.
func TestUnsubscribePipelineTrackReplacementRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		sub := &fakeSubscription{}
		old := &activePipeline{publication: sub}
		replacement, _ := newTestPipeline("TR_same")
		agent := newTestAgent(map[string]*activePipeline{})

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent.unsubscribePipelineTrack("user-1", old)
		}()

		agent.lock.Lock()
		agent.activeUserTasks["user-1"] = []byte("{}")
		agent.activePipelines["user-1"] = replacement
		agent.lock.Unlock()
		wg.Wait()

		if agent.activePipelines["user-1"] != replacement {
			t.Fatal("the replacement pipeline must remain registered")
		}
		if _, ok := agent.activeUserTasks["user-1"]; !ok {
			t.Fatal("the replacement activation must remain intact")
		}
	}
}
