package insightsservice

import (
	"sync"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

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
