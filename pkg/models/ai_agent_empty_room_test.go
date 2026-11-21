package models

import (
	"context"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

const emptyRoomTestRoomId = "room-1"

// stoppingAgent records the stop reasons of a hosted agent.
type stoppingAgent struct {
	recordingAgentSender

	reasons []string
}

func (s *stoppingAgent) Stop(reason string) { s.reasons = append(s.reasons, reason) }

// emptyRoomTestModel returns a model whose hosted agent and breakout room
// teardown are both recorded.
func emptyRoomTestModel(t *testing.T) (*AIAgentModel, *stoppingAgent, *[]string) {
	t.Helper()

	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	model.stopTimers = make(map[string]*time.Timer)
	agent := new(stoppingAgent)
	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return agent, true
	}
	closedWith := new([]string)
	model.closeBreakoutRoom = func(roomId, reason string) {
		*closedWith = append(*closedWith, reason)
	}
	t.Cleanup(func() { model.cancelStopTimer(emptyRoomTestRoomId) })

	return model, agent, closedWith
}

func TestOnRoomEmptyClosesAnIntentionalLeaveAtOnce(t *testing.T) {
	model, agent, closedWith := emptyRoomTestModel(t)
	model.onRoomEmpty(emptyRoomTestRoomId, true)

	if len(agent.reasons) != 1 || agent.reasons[0] != aiAgentStopReasonEmptyRoom {
		t.Fatalf("stop reasons = %v, want one %q", agent.reasons, aiAgentStopReasonEmptyRoom)
	}
	// the stopped agent closes the room itself, via afterAgentStopped
	if len(*closedWith) != 0 {
		t.Errorf("closed rooms = %v, want none closing the room twice", *closedWith)
	}
	if len(model.stopTimers) != 0 {
		t.Errorf("pending stop timers = %d, want none", len(model.stopTimers))
	}
}

func TestOnRoomEmptyKeepsTheGracePeriodForADropout(t *testing.T) {
	model, agent, closedWith := emptyRoomTestModel(t)
	model.onRoomEmpty(emptyRoomTestRoomId, false)

	if len(agent.reasons) != 0 {
		t.Errorf("stop reasons = %v, want the agent to keep running", agent.reasons)
	}
	if len(*closedWith) != 0 {
		t.Errorf("closed rooms = %v, want none during the grace period", *closedWith)
	}
	if len(model.stopTimers) != 1 {
		t.Errorf("pending stop timers = %d, want the empty-room grace timer", len(model.stopTimers))
	}
}

func TestStopEmptyRoomClosesTheRoomWithoutAHostedAgent(t *testing.T) {
	model, _, closedWith := emptyRoomTestModel(t)
	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return nil, false
	}
	model.startStopTimer(emptyRoomTestRoomId)

	model.stopEmptyRoom(emptyRoomTestRoomId)

	if len(*closedWith) != 1 || (*closedWith)[0] != aiAgentStopReasonEmptyRoom {
		t.Fatalf("closed rooms = %v, want one %q", *closedWith, aiAgentStopReasonEmptyRoom)
	}
	if len(model.stopTimers) != 0 {
		t.Errorf("pending stop timers = %d, want the timer cancelled", len(model.stopTimers))
	}
}

func TestStopEndsOnlyTheHostedAgent(t *testing.T) {
	model, agent, closedWith := emptyRoomTestModel(t)

	if err := model.Stop(context.Background(), emptyRoomTestRoomId, "room_ended"); err != nil {
		t.Fatalf("Stop() = %v, want nil", err)
	}
	if len(agent.reasons) != 1 || agent.reasons[0] != "room_ended" {
		t.Fatalf("stop reasons = %v, want one room_ended", agent.reasons)
	}

	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return nil, false
	}
	if err := model.Stop(context.Background(), emptyRoomTestRoomId, aiAgentStopReasonEmptyRoom); err != nil {
		t.Errorf("Stop() without a hosted agent = %v, want nil", err)
	}
	if len(agent.reasons) != 1 {
		t.Errorf("stop reasons = %v, want no new reason", agent.reasons)
	}
	// stopping is not a teardown: only the empty-room path closes the room
	if len(*closedWith) != 0 {
		t.Errorf("closed rooms = %v, want none", *closedWith)
	}
}

func TestOnUserLeftIgnoresInternalUsers(t *testing.T) {
	model, agent, closedWith := emptyRoomTestModel(t)

	model.OnUserLeft(context.Background(), emptyRoomTestRoomId, config.AgentUserUserIdPrefix+"1", true)

	if len(agent.reasons) != 0 || len(*closedWith) != 0 || len(model.stopTimers) != 0 {
		t.Errorf("internal user stopped the session: reasons=%v closed=%v timers=%d", agent.reasons, *closedWith, len(model.stopTimers))
	}
}
