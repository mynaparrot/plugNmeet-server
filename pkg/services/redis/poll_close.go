package redisservice

import (
	"strconv"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

// pollCloseScript atomically flips a running poll to closed. Go supplies the
// updated data payload (with closed_by recorded); the operational
// is_running/expires_at/is_anonymous fields stay authoritative. Preserves the
// votes. Keys: meta, voters, results, room index, pollsWithDuration hash.
// A successful transition refreshes every poll key's TTL. Returns:
// 1=transitioned, 0=already closed (no TTL refresh), -1=poll not found.
var pollCloseScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
	return -1
end
if redis.call("HGET", KEYS[1], "` + PollIsRunningField + `") ~= "1" then
	return 0
end
redis.call("HSET", KEYS[1], "` + PollDataField + `", ARGV[1], "` + PollIsRunningField + `", "0")
redis.call("HDEL", KEYS[5], ARGV[2])
for i = 1, 4 do
	redis.call("EXPIRE", KEYS[i], ARGV[3])
end
return 1
`)

// ClosePollIfRunning closes a running poll with the given closed_by marker and
// purges its duration-index entry; returns true only when it made the
// running->closed flip (idempotent no-op when already closed).
func (s *RedisService) ClosePollIfRunning(roomId, pollId, closedBy string) (bool, error) {
	info, err := s.GetPollInfoByPollId(roomId, pollId)
	switch {
	case err != nil:
		return false, err
	case info == nil:
		return false, config.ErrPollNotFound
	case !info.GetIsRunning():
		return false, nil
	}
	info.ClosedBy = closedBy
	info.IsRunning = false
	data, err := protojson.Marshal(info)
	if err != nil {
		return false, err
	}

	res, err := pollCloseScript.Run(s.ctx, s.rc, []string{
		pollMetaKey(roomId, pollId),
		pollVotersKey(roomId, pollId),
		pollResultsKey(roomId, pollId),
		pollRoomIndexKey(roomId),
		pollsWithDurationKey,
	}, string(data), pollDurationIndexField(roomId, pollId),
		int(s.defaultTTL.Seconds())).Int64()
	if err != nil {
		return false, err
	}
	switch res {
	case -1:
		return false, config.ErrPollNotFound
	case 1:
		return true, nil
	}
	return false, nil
}

// pollReopenScript atomically reopens a closed poll: it stores the Go-supplied
// data payload, restarts expires_at based on the poll's duration, and refreshes
// the duration-index entry. Stored votes are kept untouched. Keys: meta,
// pollsWithDuration hash, voters, results, room index. A successful
// transition refreshes every poll key's TTL — including the global
// duration-index hash, so the renewed poll outlives a stale index entry.
// Returns: 1=transitioned, 0=already running (no TTL refresh), -1=poll not
// found.
var pollReopenScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then
	return -1
end
if redis.call("HGET", KEYS[1], "` + PollIsRunningField + `") == "1" then
	return 0
end
local dur = tonumber(ARGV[2]) or 0
local exp = 0
if dur > 0 then
	exp = tonumber(ARGV[3]) + dur
	redis.call("HSET", KEYS[2], ARGV[4], exp)
	redis.call("EXPIRE", KEYS[2], ARGV[5])
else
	redis.call("HDEL", KEYS[2], ARGV[4])
end
for i = 1, 5 do
	redis.call("EXPIRE", KEYS[i], ARGV[5])
end
redis.call("HSET", KEYS[1], "` + PollDataField + `", ARGV[1], "` + PollIsRunningField + `", "1",
	"` + PollExpiresAtField + `", exp)
return 1
`)

// ReopenPollIfClosed reopens a closed poll, preserving every stored vote and
// restarting its expires_at; returns the poll's duration and whether the
// closed->running flip actually happened. ErrPollNotFound surfaces when the
// poll is gone.
func (s *RedisService) ReopenPollIfClosed(r *plugnmeet.ReopenPollReq) (uint32, bool, error) {
	info, err := s.GetPollInfoByPollId(r.RoomId, r.PollId)
	switch {
	case err != nil:
		return 0, false, err
	case info == nil:
		return 0, false, config.ErrPollNotFound
	}
	info.ClosedBy = ""
	info.IsRunning = true
	data, err := protojson.Marshal(info)
	if err != nil {
		return 0, false, err
	}

	res, err := pollReopenScript.Run(s.ctx, s.rc, []string{
		pollMetaKey(r.RoomId, r.PollId),
		pollsWithDurationKey,
		pollVotersKey(r.RoomId, r.PollId),
		pollResultsKey(r.RoomId, r.PollId),
		pollRoomIndexKey(r.RoomId),
	}, string(data),
		strconv.FormatUint(uint64(info.GetDuration()), 10),
		strconv.FormatInt(time.Now().Unix(), 10),
		pollDurationIndexField(r.RoomId, r.PollId),
		int(s.defaultTTL.Seconds())).Int64()
	if err != nil {
		return 0, false, err
	}
	switch res {
	case -1:
		return 0, false, config.ErrPollNotFound
	case 1:
		return info.GetDuration(), true, nil
	}
	return info.GetDuration(), false, nil
}

// CleanUpPolls removes every poll key for the room and its index.
func (s *RedisService) CleanUpPolls(roomId string, pollIds []string) error {
	pp := s.rc.Pipeline()
	indexKey := pollRoomIndexKey(roomId)

	for _, id := range pollIds {
		pp.Del(s.ctx, pollMetaKey(roomId, id))
		pp.Del(s.ctx, pollVotersKey(roomId, id))
		pp.Del(s.ctx, pollResultsKey(roomId, id))
		// purge the room's duration index entries along with the polls
		pp.HDel(s.ctx, pollsWithDurationKey, pollDurationIndexField(roomId, id))
	}
	pp.Del(s.ctx, indexKey)

	_, err := pp.Exec(s.ctx)
	return err
}
