package redisservice

import (
	"context"
	"math"
	"testing"
	"time"
)

func liveCostEquals(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestInsightsLiveCostRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	roomId := "room-live-cost"

	if costs, err := s.GetInsightsLiveCost(ctx, roomId); err != nil || len(costs) != 0 {
		t.Fatalf("a room without usage must report no cost (%v, err %v)", costs, err)
	}

	if err := s.SetInsightsLiveCost(ctx, roomId, LiveCostFieldTranscription, 1.25); err != nil {
		t.Fatal(err)
	}
	// a delta is added on top, and a non-positive one books nothing
	for _, delta := range []float64{0.5, 0.25, -1} {
		if err := s.IncrInsightsLiveCost(ctx, roomId, LiveCostFieldTTS, delta); err != nil {
			t.Fatalf("delta %v: %v", delta, err)
		}
	}

	costs, err := s.GetInsightsLiveCost(ctx, roomId)
	if err != nil {
		t.Fatal(err)
	}
	if !liveCostEquals(costs[LiveCostFieldTranscription], 1.25) {
		t.Errorf("transcription = %v, want 1.25", costs[LiveCostFieldTranscription])
	}
	if !liveCostEquals(costs[LiveCostFieldTTS], 0.75) {
		t.Errorf("tts = %v, want 0.75", costs[LiveCostFieldTTS])
	}

	if member, err := rc.SIsMember(ctx, InsightsActiveRoomsKey, roomId).Result(); err != nil || !member {
		t.Fatalf("the room must be registered as active (member %v, err %v)", member, err)
	}
	for _, key := range []string{liveCostKey(roomId), InsightsActiveRoomsKey} {
		if ttl, err := rc.TTL(ctx, key).Result(); err != nil || ttl <= 0 || ttl > s.defaultTTL {
			t.Errorf("ttl of %s = %s (err %v), want at most %s", key, ttl, err, s.defaultTTL)
		}
	}

	if err := s.CleanupInsightsLiveCost(ctx, roomId); err != nil {
		t.Fatal(err)
	}
	if costs, err := s.GetInsightsLiveCost(ctx, roomId); err != nil || len(costs) != 0 {
		t.Fatalf("the cost must be gone (%v, err %v)", costs, err)
	}
	if member, err := rc.SIsMember(ctx, InsightsActiveRoomsKey, roomId).Result(); err != nil || member {
		t.Fatalf("the room must not stay active (member %v, err %v)", member, err)
	}

	// a zero delta still registers the room, so a reader sees it running
	if err := s.IncrInsightsLiveCost(ctx, roomId, LiveCostFieldAIAgent, 0); err != nil {
		t.Fatal(err)
	}
	if member, err := rc.SIsMember(ctx, InsightsActiveRoomsKey, roomId).Result(); err != nil || !member {
		t.Fatalf("a zero delta must register the room (member %v, err %v)", member, err)
	}
	if costs, _ := s.GetInsightsLiveCost(ctx, roomId); len(costs) != 0 {
		t.Errorf("a zero delta must not book a cost: %v", costs)
	}
}

// The ai agent telemetry flush carries the session's live cost to Redis while it
// is still running.
func TestSaveAIAgentSessionTelemetryMirrorsLiveCost(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	roomId := "room-agent-cost"

	telemetry := AIAgentSessionTelemetry{SessionID: "s1", EstimatedTokenCost: 0.25, EstimatedAvatarCost: 0.5}
	if err := s.SaveAIAgentSessionTelemetry(ctx, roomId, telemetry); err != nil {
		t.Fatal(err)
	}

	costs, err := s.GetInsightsLiveCost(ctx, roomId)
	if err != nil {
		t.Fatal(err)
	}
	if !liveCostEquals(costs[LiveCostFieldAIAgent], 0.25) || !liveCostEquals(costs[LiveCostFieldAIAgentAvatar], 0.5) {
		t.Fatalf("live cost = %v, want the session cost of 0.25 + 0.5", costs)
	}
	if member, err := rc.SIsMember(ctx, InsightsActiveRoomsKey, roomId).Result(); err != nil || !member {
		t.Fatalf("the room must be registered as active (member %v, err %v)", member, err)
	}
	if ttl, err := rc.TTL(ctx, aiAgentTelemetryKey(roomId)).Result(); err != nil || ttl <= 0 || ttl > s.defaultTTL {
		t.Errorf("telemetry ttl = %s (err %v), want at most %s", ttl, err, s.defaultTTL)
	}
}

// A closed session without any cost keeps the stored telemetry but no live cost.
func TestSaveAIAgentSessionTelemetryEndedWithoutCost(t *testing.T) {
	s, _ := newMCPTestService(t)
	ctx := context.Background()
	roomId := "room-agent-ended"

	telemetry := AIAgentSessionTelemetry{SessionID: "s1", EndedAt: time.Now().UnixMilli()}
	if err := s.SaveAIAgentSessionTelemetry(ctx, roomId, telemetry); err != nil {
		t.Fatal(err)
	}

	if costs, err := s.GetInsightsLiveCost(ctx, roomId); err != nil || len(costs) != 0 {
		t.Fatalf("an ended session without cost must book nothing (%v, err %v)", costs, err)
	}
	if saved, err := s.GetAIAgentSessionTelemetry(ctx, roomId); err != nil || saved == nil {
		t.Fatalf("the telemetry was not stored (%+v, err %v)", saved, err)
	}
}
