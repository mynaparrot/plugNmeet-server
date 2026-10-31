package redisservice

import (
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

// CreateRoomPoll writes one poll's metadata hash, pre-creates its results hash
// (so a zero counter is always readable), and registers the poll id in the
// room's index.
func (s *RedisService) CreateRoomPoll(roomId string, info *plugnmeet.PollInfo) error {
	fields, err := pollToMetaFields(info)
	if err != nil {
		return err
	}
	metaKey, resultsKey, indexKey :=
		pollMetaKey(roomId, info.GetId()), pollResultsKey(roomId, info.GetId()), pollRoomIndexKey(roomId)

	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, metaKey, fields)
	pipe.Expire(s.ctx, metaKey, s.defaultTTL)
	pipe.HSet(s.ctx, resultsKey, PollTotalRespField, "0")
	pipe.Expire(s.ctx, resultsKey, s.defaultTTL)
	// the voters hash is created by the first vote; pre-expiring an empty key
	// is a no-op so it is intentionally skipped
	pipe.SAdd(s.ctx, indexKey, info.GetId())
	pipe.Expire(s.ctx, indexKey, s.defaultTTL)

	_, err = pipe.Exec(s.ctx)
	return err
}

// CreateRoomPolls registers several polls (e.g. shared parent polls copied into
// a breakout room); per-poll failures are reported only through the returned
// error to keep the API small.
func (s *RedisService) CreateRoomPolls(roomId string, infos []*plugnmeet.PollInfo) error {
	pipe := s.rc.Pipeline()
	indexKey := pollRoomIndexKey(roomId)

	for _, info := range infos {
		fields, err := pollToMetaFields(info)
		if err != nil {
			return err
		}
		metaKey, resultsKey := pollMetaKey(roomId, info.GetId()), pollResultsKey(roomId, info.GetId())
		pipe.HSet(s.ctx, metaKey, fields)
		pipe.Expire(s.ctx, metaKey, s.defaultTTL)
		pipe.HSet(s.ctx, resultsKey, PollTotalRespField, "0")
		pipe.Expire(s.ctx, resultsKey, s.defaultTTL)
		pipe.SAdd(s.ctx, indexKey, info.GetId())
	}
	pipe.Expire(s.ctx, indexKey, s.defaultTTL)

	_, err := pipe.Exec(s.ctx)
	return err
}

// AddPollWithDuration registers a fixed-duration poll in the
// pollsWithDuration index (value: the poll's expires_at as unix seconds) so
// the janitor can sweep for expired polls.
func (s *RedisService) AddPollWithDuration(roomId, pollId string, expiresAt int64) error {
	pipe := s.rc.Pipeline()
	pipe.HSet(s.ctx, pollsWithDurationKey, pollDurationIndexField(roomId, pollId), expiresAt)
	pipe.HExpire(s.ctx, pollsWithDurationKey, s.defaultTTL, pollDurationIndexField(roomId, pollId))

	_, err := pipe.Exec(s.ctx)
	return err
}

// RemovePollWithDuration drops a poll's entry from the pollsWithDuration index.
func (s *RedisService) RemovePollWithDuration(roomId, pollId string) error {
	return s.rc.HDel(s.ctx, pollsWithDurationKey, pollDurationIndexField(roomId, pollId)).Err()
}

// GetPollsWithDuration returns every entry of the pollsWithDuration index.
func (s *RedisService) GetPollsWithDuration() (map[string]string, error) {
	return s.rc.HGetAll(s.ctx, pollsWithDurationKey).Result()
}

// pollVoteScript atomically rejects closed/expired polls and duplicate voters,
// then stores the respondent value, bumps the total and the per-answer counters,
// and refreshes the related TTLs. No JSON is parsed and no keys are validated
// here; Go owns answer and structure validation.
// ARGV[3] carries the marshalled non-anonymous PollRespondent record; the
// script decides between it and the ARGV[4] anonymous marker based on the
// authoritative is_anonymous metadata field. ARGV[6] selects the tally mode:
// "opt" counts ARGV[7..] option ids, "txt" counts the normalized ARGV[7] key
// and remembers the first ARGV[8] display text.
// Returns: 0=ok, 1=poll closed or expired, 2=already voted, 3=poll not found.
var pollVoteScript = redis.NewScript(`
local running = redis.call("HGET", KEYS[1], "` + PollIsRunningField + `")
if running == false then
	return 3
end
if running ~= "1" then
	return 1
end
local exp = tonumber(redis.call("HGET", KEYS[1], "` + PollExpiresAtField + `") or "0")
if exp and exp > 0 and tonumber(ARGV[2]) >= exp then
	return 1
end
if redis.call("HEXISTS", KEYS[2], ARGV[1]) == 1 then
	return 2
end
local val = ARGV[3]
if redis.call("HGET", KEYS[1], "` + PollIsAnonymousField + `") == "1" then
	val = ARGV[4]
end
redis.call("HSET", KEYS[2], ARGV[1], val)
redis.call("HINCRBY", KEYS[3], "` + PollTotalRespField + `", 1)
if ARGV[6] == "txt" then
	redis.call("HINCRBY", KEYS[3], "` + PollTextCountPrefix + `" .. ARGV[7], 1)
	redis.call("HSETNX", KEYS[3], "` + PollTextDisplayPrefix + `" .. ARGV[7], ARGV[8])
else
	for i = 7, #ARGV do
		redis.call("HINCRBY", KEYS[3], ARGV[i] .. "` + PollCountSuffix + `", 1)
	end
end
local ttl = tonumber(ARGV[5])
for i = 1, 4 do
	redis.call("EXPIRE", KEYS[i], ttl)
end
return 0
`)

// AddPollResponse records one vote through pollVoteScript. Go has already
// validated the answer; the Lua script enforces is_running/expires_at and the
// one-vote-per-voter rule atomically, choosing between the marshalled
// respondent record and the anonymous marker via the metadata hash.
func (s *RedisService) AddPollResponse(r *plugnmeet.SubmitPollResponseReq) error {
	respondentValue := PollAnonymousMarker
	textAnswer := strings.TrimSpace(r.TextAnswer)
	marshalled, err := protojson.Marshal(&plugnmeet.PollRespondent{
		UserId:     r.UserId,
		Name:       r.Name,
		OptionIds:  r.SelectedOptions,
		TextAnswer: textAnswer,
	})
	if err != nil {
		return err
	}

	opts := make([]interface{}, 0, 8)
	opts = append(opts, r.UserId, strconv.FormatInt(time.Now().Unix(), 10),
		// ARGV[3]: non-anonymous record, ARGV[4]: anonymous marker
		string(marshalled), respondentValue,
		int(s.defaultTTL.Seconds()))
	if textAnswer != "" {
		// ARGV[6]="txt", ARGV[7]=normalized key, ARGV[8]=display text
		opts = append(opts, "txt", strings.ToLower(textAnswer), textAnswer)
	} else {
		opts = append(opts, "opt")
		for _, id := range r.SelectedOptions {
			opts = append(opts, strconv.FormatUint(id, 10))
		}
	}

	roomId := r.RoomId
	res, err := pollVoteScript.Run(s.ctx, s.rc, []string{
		pollMetaKey(roomId, r.PollId),
		pollVotersKey(roomId, r.PollId),
		pollResultsKey(roomId, r.PollId),
		pollRoomIndexKey(roomId),
	}, opts...).Int64()
	if err != nil {
		return err
	}
	switch res {
	case 1:
		return config.ErrPollClosed
	case 2:
		return config.ErrPollAlreadyVoted
	case 3:
		return config.ErrPollNotFound
	}
	return nil
}
