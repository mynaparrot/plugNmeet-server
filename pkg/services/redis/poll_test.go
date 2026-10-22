package redisservice

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

func redisClientForTest(t *testing.T, addr string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr})
	if err := c.Ping(context.Background()).Err(); err != nil {
		t.Skipf("no redis fixture at %s: %v", addr, err)
	}
	return c
}

func newTestService(t *testing.T) *RedisService {
	t.Helper()
	host := os.Getenv("TEST_REDIS_HOST")
	if host == "" {
		host = "localhost:6379"
	}
	rc := redisClientForTest(t, host)
	client := &RedisService{
		ctx:        context.Background(),
		rc:         rc,
		defaultTTL: 2 * time.Minute,
		logger:     logrus.New().WithField("test", "redisservice"),
	}
	t.Cleanup(func() {
		ids, _ := client.GetPollIdsByRoomId(testRoomId)
		for _, id := range ids {
			_ = client.CleanUpPolls(testRoomId, []string{id})
		}
	})
	return client
}

func newPoll(roomId string, running bool) *plugnmeet.PollInfo {
	return &plugnmeet.PollInfo{

		Id:          "poll-" + roomId,
		RoomId:      roomId,
		Question:    "q-üñïtçode: ok",
		Options:     []*plugnmeet.CreatePollOptions{{Id: 1, Text: "a"}, {Id: 2, Text: "b"}},
		IsRunning:   running,
		Created:     time.Now().Unix(),
		CreatedBy:   "creator",
		IsAnonymous: false,
		ExpiresAt:   time.Now().Unix() + 600,
	}
}

func TestPollCreateMetaLayout(t *testing.T) {
	s := newTestService(t)
	info := newPoll(testRoomId, true)
	info.IsAnonymous = true
	if err := s.CreateRoomPoll(testRoomId, info); err != nil {
		t.Fatal(err)
	}

	fields, err := s.rc.HGetAll(s.ctx, pollMetaKey(testRoomId, info.Id)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 4 {
		t.Fatalf("expected exactly 4 meta fields (data,is_running,expires_at,is_anonymous), got %v", fields)
	}
	for _, f := range []string{PollDataField, PollIsRunningField, PollExpiresAtField, PollIsAnonymousField} {
		if _, ok := fields[f]; !ok {
			t.Fatalf("missing field %s in %v", f, fields)
		}
	}

	got, err := s.GetPollInfoByPollId(testRoomId, info.Id)
	if err != nil || got == nil {
		t.Fatalf("read back failed: %v %v", got, err)
	}
	// stale data must not override the operational fields
	if !got.IsRunning || !got.IsAnonymous || got.ExpiresAt < time.Now().Unix() {
		t.Fatalf("overlay failed: %+v", got)
	}
}

func TestPollDuplicateAndConcurrentVoting(t *testing.T) {
	s := newTestService(t)
	info := newPoll(testRoomId, true)
	if err := s.CreateRoomPoll(testRoomId, info); err != nil {
		t.Fatal(err)
	}
	req := &plugnmeet.SubmitPollResponseReq{
		RoomId: testRoomId, PollId: info.Id, UserId: "user1", Name: "U 1",
		SelectedOptions: []uint64{1},
	}
	if err := s.AddPollResponse(req); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPollResponse(req); !errors.Is(err, config.ErrPollAlreadyVoted) {
		t.Fatalf("expected duplicate-vote error, got %v", err)
	}

	// concurrent voters on the same option; only two should succeed
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r2 := &plugnmeet.SubmitPollResponseReq{
				RoomId: testRoomId, PollId: info.Id, UserId: "user2", Name: "U 2",
				SelectedOptions: []uint64{2},
			}
			if i < 2 {
				r2.UserId, r2.Name = "user2", "U 2"
			} else {
				r2.UserId, r2.Name = "user3", "U 3"
			}
			errs[i] = s.AddPollResponse(r2)
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil && !errors.Is(e, config.ErrPollAlreadyVoted) {
			t.Fatalf("unexpected vote error: %v", e)
		}
	}

	total, err := s.GetPollTotalResponses(testRoomId, info.Id)
	if err != nil || total != 3 {
		t.Fatalf("total responses = %d err=%v, want 3", total, err)
	}
	counters, _ := s.GetPollResultsRaw(testRoomId, info.Id)
	if counters["total_resp"] != "3" || counters["1_count"] != "1" || counters["2_count"] != "2" {
		t.Fatalf("wrong counters: %v", counters)
	}

	r, err := s.GetPollRespondent(testRoomId, info.Id, "user1")
	if err != nil || r.Name != "U 1" || len(r.OptionIds) != 1 {
		t.Fatalf("respondent readback failed: %v %v", r, err)
	}
}

func TestPollAnonymousStoresNoChoiceOrName(t *testing.T) {
	s := newTestService(t)
	info := newPoll(testRoomId, true)
	info.IsAnonymous = true
	if err := s.CreateRoomPoll(testRoomId, info); err != nil {
		t.Fatal(err)
	}
	req := &plugnmeet.SubmitPollResponseReq{
		RoomId: testRoomId, PollId: info.Id, UserId: "secret", Name: "Secret Name",
		SelectedOptions: []uint64{1},
	}
	if err := s.AddPollResponse(req); err != nil {
		t.Fatal(err)
	}

	raw, err := s.rc.HGet(s.ctx, pollVotersKey(testRoomId, info.Id), "secret").Result()
	if err != nil {
		t.Fatal(err)
	}
	if raw != PollAnonymousMarker {
		t.Fatalf("anonymous vote stored undisclosed data: %q", raw)
	}

	details, err := s.GetPollResponsesDetails(testRoomId, info.Id, info)
	if err != nil {
		t.Fatal(err)
	}
	if details.TotalRespondents != 1 || len(details.Respondents) != 0 {
		t.Fatalf("anonymous details must keep counts only: %+v", details)
	}
}

func TestPollClosedDeadlineAndReopen(t *testing.T) {
	s := newTestService(t)
	// expired deadline in the past rejects a vote even while is_running=1
	info := newPoll(testRoomId, true)
	info.ExpiresAt = time.Now().Unix() - 60
	if err := s.CreateRoomPoll(testRoomId, info); err != nil {
		t.Fatal(err)
	}
	req := &plugnmeet.SubmitPollResponseReq{RoomId: testRoomId, PollId: info.Id, UserId: "u", SelectedOptions: []uint64{1}}

	info2 := newPoll(testRoomId, true)
	if err := s.CreateRoomPoll(testRoomId, info2); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPollResponse(&plugnmeet.SubmitPollResponseReq{
		RoomId: testRoomId, PollId: info2.Id, UserId: "u", Name: "n", SelectedOptions: []uint64{1},
	}); err != nil {
		t.Fatal(err)
	}
	// use the fresh poll from here on
	info = info2
	closed, err := s.ClosePollIfRunning(testRoomId, info.Id, "mod1")
	if err != nil || !closed {
		t.Fatalf("close failed: %v %v", closed, err)
	}
	// idempotent
	if closed, err := s.ClosePollIfRunning(testRoomId, info.Id, "mod1"); closed || err != nil {
		t.Fatalf("second close should be a no-op: %v %v", closed, err)
	}
	// unknown poll
	if closed, err := s.ClosePollIfRunning(testRoomId, "missing", "mod1"); closed || !errors.Is(err, config.ErrPollNotFound) {
		t.Fatalf("expected not found: %v %v", closed, err)
	}
	if err := s.AddPollResponse(req); !errors.Is(err, config.ErrPollClosed) {
		t.Fatalf("voting on a closed poll must fail: %v", err)
	}
	if err := s.AddPollResponse(&plugnmeet.SubmitPollResponseReq{RoomId: testRoomId, PollId: "missing", UserId: "u"}); !errors.Is(err, config.ErrPollNotFound) {
		t.Fatal("vote on missing poll should error")
	}

	// reopen preserves votes and restarts the deadline
	dur, reopened, err := s.ReopenPollIfClosed(&plugnmeet.ReopenPollReq{RoomId: testRoomId, PollId: info.Id})
	if err != nil || !reopened {
		t.Fatalf("reopen failed: %v %v %v", dur, reopened, err)
	}
	got, _ := s.GetPollInfoByPollId(testRoomId, info.Id)
	if !got.IsRunning || got.ClosedBy != "" {
		t.Fatalf("reopened poll metadata wrong: %+v", got)
	}
	respondent, _ := s.GetPollRespondent(testRoomId, info.Id, "u")
	if respondent == nil {
		t.Fatal("reopen must preserve stored votes")
	}
	total, _ := s.GetPollTotalResponses(testRoomId, info.Id)
	if total != 1 {
		t.Fatalf("vote counter lost on reopen: %d", total)
	}
	// already running: idempotent no-op
	if _, reopened, err := s.ReopenPollIfClosed(&plugnmeet.ReopenPollReq{RoomId: testRoomId, PollId: info.Id}); reopened || err != nil {
		t.Fatalf("second reopen should be a no-op: %v %v", reopened, err)
	}
}

func TestPollLifecycleTTLRefreshAndCleanup(t *testing.T) {
	s := newTestService(t)
	info := newPoll(testRoomId, true)
	info.Duration = 600
	info.ExpiresAt = time.Now().Unix() + 600
	if err := s.CreateRoomPoll(testRoomId, info); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPollWithDuration(testRoomId, info.Id, info.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	ttlOf := func(key string) time.Duration {
		t.Helper()
		d, err := s.rc.TTL(s.ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	// close refreshes meta/voters/results/room-index TTLs
	if closed, err := s.ClosePollIfRunning(testRoomId, info.Id, "mod"); err != nil || !closed {
		t.Fatalf("close failed: %v %v", closed, err)
	}
	for _, key := range []string{
		pollMetaKey(testRoomId, info.Id),
		pollResultsKey(testRoomId, info.Id),
		pollRoomIndexKey(testRoomId),
	} {
		if ttlOf(key) <= 0 {
			t.Fatalf("TTL not refreshed on close for %s", key)
		}
	}
	got, err := s.GetPollInfoByPollId(testRoomId, info.Id)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	// vote, reopen and verify voter hash TTL refresh
	if err := s.rc.HSet(s.ctx, pollVotersKey(testRoomId, info.Id), "u", "x").Err(); err != nil {
		t.Fatal(err)
	}
	if _, reopened, err := s.ReopenPollIfClosed(&plugnmeet.ReopenPollReq{RoomId: testRoomId, PollId: info.Id}); err != nil || !reopened {
		t.Fatalf("reopen failed: %v %v", reopened, err)
	}
	for _, key := range []string{
		pollMetaKey(testRoomId, info.Id),
		pollVotersKey(testRoomId, info.Id),
		pollResultsKey(testRoomId, info.Id),
		pollRoomIndexKey(testRoomId),
		pollsWithDurationKey,
	} {
		if ttlOf(key) <= 0 {
			t.Fatalf("TTL not refreshed on reopen for %s", key)
		}
	}
	// duration-index entry must have been recreated for the renewed poll
	idx, err := s.GetPollsWithDuration()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx[pollDurationIndexField(testRoomId, info.Id)]; !ok {
		t.Fatal("duration index entry missing after reopen")
	}

	// cleanup removes every poll key and the room index
	if err := s.CleanUpPolls(testRoomId, []string{info.Id}); err != nil {
		t.Fatal(err)
	}
	n, err := s.rc.Exists(s.ctx, pollMetaKey(testRoomId, info.Id),
		pollVotersKey(testRoomId, info.Id), pollResultsKey(testRoomId, info.Id),
		pollRoomIndexKey(testRoomId)).Result()
	if err != nil || n != 0 {
		t.Fatalf("cleanup left %d keys behind: %v", n, err)
	}
}

const testRoomId = "t-poll-room"
