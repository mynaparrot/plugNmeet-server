package insightsservice

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// newLiveCostStore returns a telemetry store backed by miniredis.
func newLiveCostStore(t *testing.T) *redisservice.RedisService {
	t.Helper()
	mr := miniredis.RunT(t)
	ttl := time.Hour
	return redisservice.New(redisservice.Args{
		Ctx:    context.Background(),
		Rc:     redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		App:    &config.AppConfig{RedisInfo: connections.RedisInfo{DefaultTTL: &ttl}},
		Logger: logrus.New(),
	})
}

// A usage event must reach the live cost of the room at once, and so must the
// periodic flush of a session without any new turn.
func TestAIAgentLiveCostFollowsUsageAndFlush(t *testing.T) {
	store := newLiveCostStore(t)
	ctx := context.Background()

	env := newAgentTestEnv(t, nil, func(args *aiAgentCoreArgs) {
		args.RedisService = store
		// A dollar per million input tokens, a dollar per minute of avatar.
		args.EstimateCost = func(snap *redisservice.AIAgentSessionTelemetry) (float64, float64) {
			return float64(snap.InputTokens) / 1000000, float64(snap.AvatarMillis) / 60000
		}
	})
	roomId := env.agent.RoomID()

	// The opening flush registers the room with a zero cost.
	costs, err := store.GetInsightsLiveCost(ctx, roomId)
	if err != nil {
		t.Fatal(err)
	}
	if len(costs) == 0 {
		t.Fatalf("the opening flush did not register the session cost: %v", costs)
	}

	env.session.Push(insights.RealtimeEvent{Type: insights.RealtimeEventUsage, InputTokens: 2000000})
	waitFor(t, func() bool {
		costs, err := store.GetInsightsLiveCost(ctx, roomId)
		return err == nil && math.Abs(costs[redisservice.LiveCostFieldAIAgent]-2) < 1e-6
	}, "the live cost of the usage event")

	saved, err := store.GetAIAgentSessionTelemetry(ctx, roomId)
	if err != nil || saved == nil {
		t.Fatalf("the telemetry was not saved (%+v, err %v)", saved, err)
	}
	if saved.EstimatedTokenCost != 2 || saved.EstimatedTotalCost != 2 {
		t.Fatalf("saved estimate = %+v, want 2 token cost", saved)
	}

	// A flush alone must push the current state, e.g. an avatar running silently.
	env.agent.avatarStartedAt = time.Now().Add(-2 * time.Minute)
	env.agent.FlushTelemetry()

	costs, err = store.GetInsightsLiveCost(ctx, roomId)
	if err != nil {
		t.Fatal(err)
	}
	if avatar := costs[redisservice.LiveCostFieldAIAgentAvatar]; math.Abs(avatar-2) > 1e-3 {
		t.Fatalf("avatar cost = %v, want the 2 minutes of generated video", avatar)
	}

	saved, err = store.GetAIAgentSessionTelemetry(ctx, roomId)
	if err != nil || saved == nil {
		t.Fatalf("the telemetry was not saved (%+v, err %v)", saved, err)
	}
	if saved.EstimatedAvatarCost < 1.99 || saved.EstimatedTotalCost < 3.99 {
		t.Fatalf("saved estimate = %+v, want the avatar minutes added", saved)
	}
}
