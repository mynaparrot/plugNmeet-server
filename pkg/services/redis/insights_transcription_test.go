package redisservice

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func TestFlushTranscriptionUsageAccruesAndAdvancesStart(t *testing.T) {
	s, rc := newMCPTestService(t)
	roomId, userId := "room-flush", "user-1"
	ctx := context.Background()
	sessionsKey := fmt.Sprintf(TranscriptionSessionsKey, roomId)

	// Without a session there is nothing to accrue.
	if dur, err := s.FlushTranscriptionUsage(roomId, userId); err != nil || dur != 0 {
		t.Fatalf("flush without a session = (%d, %v), want (0, nil)", dur, err)
	}

	start := time.Now().Unix() - 30
	rc.HSet(s.ctx, sessionsKey, userId, start)

	dur, err := s.FlushTranscriptionUsage(roomId, userId)
	if err != nil {
		t.Fatal(err)
	}
	if dur < 30 || dur > 32 {
		t.Fatalf("flushed %d seconds, want the ~30 accrued", dur)
	}

	userUsage, err := s.GetTranscriptionUserUsage(ctx, roomId, userId)
	if err != nil || userUsage != dur {
		t.Fatalf("user usage = (%d, %v), want %d", userUsage, err, dur)
	}
	roomUsage, err := s.GetTranscriptionRoomUsage(ctx, roomId, false)
	if err != nil {
		t.Fatal(err)
	}
	if roomUsage[TotalUsageField] != dur {
		t.Errorf("total usage = %d, want %d", roomUsage[TotalUsageField], dur)
	}

	// The start time moved forward, so a second flush books only the new seconds.
	newStartStr, err := rc.HGet(ctx, sessionsKey, userId).Result()
	if err != nil {
		t.Fatal(err)
	}
	newStart, err := strconv.ParseInt(newStartStr, 10, 64)
	if err != nil || newStart < start+30 {
		t.Fatalf("start time = %d (err %v), want it advanced past %d", newStart, err, start+30)
	}
	if dur, err := s.FlushTranscriptionUsage(roomId, userId); err != nil || dur != 0 {
		t.Fatalf("an immediate second flush = (%d, %v), want no double count", dur, err)
	}
}

func TestGetTranscriptionRoomUsageFoldsOpenSessions(t *testing.T) {
	s, rc := newMCPTestService(t)
	roomId := "room-fold"
	ctx := context.Background()
	sessionsKey := fmt.Sprintf(TranscriptionSessionsKey, roomId)
	usageKey := fmt.Sprintf(TranscriptionUsageKey, roomId)

	// user-1 stopped cleanly, user-2 is still talking when the room ends.
	rc.HSet(s.ctx, usageKey, "user-1", 10)
	rc.HSet(s.ctx, usageKey, TotalUsageField, 10)
	rc.HSet(s.ctx, sessionsKey, "user-2", time.Now().Unix()-20)

	// A plain read must not touch the running session.
	if usage, err := s.GetTranscriptionRoomUsage(ctx, roomId, false); err != nil || usage["user-2"] != 0 {
		t.Fatalf("a non-cleanup read folded the session (%v, err %v)", usage, err)
	}

	usage, err := s.GetTranscriptionRoomUsage(ctx, roomId, true)
	if err != nil {
		t.Fatal(err)
	}
	if usage["user-1"] != 10 {
		t.Errorf("user-1 = %d, want the 10 recorded seconds", usage["user-1"])
	}
	if usage["user-2"] < 20 {
		t.Errorf("user-2 = %d, want the ~20 un-flushed seconds", usage["user-2"])
	}
	if want := usage["user-1"] + usage["user-2"]; usage[TotalUsageField] != want {
		t.Errorf("total usage = %d, want %d", usage[TotalUsageField], want)
	}

	if left, err := rc.Exists(ctx, usageKey, sessionsKey).Result(); err != nil || left != 0 {
		t.Fatalf("%d keys left behind (err %v), want the usage and sessions cleaned", left, err)
	}
}
