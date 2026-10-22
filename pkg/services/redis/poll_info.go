package redisservice

import (
	"errors"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"
)

// GetPollIdsByRoomId returns the room's poll ids from its index SET.
func (s *RedisService) GetPollIdsByRoomId(roomId string) ([]string, error) {
	result, err := s.rc.SMembers(s.ctx, pollRoomIndexKey(roomId)).Result()
	return result, err
}

// GetRoomPolls returns the typed PollInfo of every poll indexed for the room.
func (s *RedisService) GetRoomPolls(roomId string) ([]*plugnmeet.PollInfo, error) {
	ids, err := s.GetPollIdsByRoomId(roomId)
	if err != nil {
		return nil, err
	}

	polls := make([]*plugnmeet.PollInfo, 0, len(ids))
	for _, id := range ids {
		info, err := s.GetPollInfoByPollId(roomId, id)
		if err != nil {
			return nil, err
		}
		if info != nil {
			polls = append(polls, info)
		}
	}
	return polls, nil
}

// GetPollInfoByPollId returns the typed PollInfo with the operational fields
// overlaid, or nil when the poll is gone.
func (s *RedisService) GetPollInfoByPollId(roomId, pollId string) (*plugnmeet.PollInfo, error) {
	result, err := s.rc.HGetAll(s.ctx, pollMetaKey(roomId, pollId)).Result()
	switch {
	case err != nil:
		return nil, err
	case len(result) == 0:
		return nil, nil
	}
	return pollFromMetaFields(result)
}

// GetPollResultsRaw returns the per-poll results hash (total_resp + counts).
func (s *RedisService) GetPollResultsRaw(roomId, pollId string) (map[string]string, error) {
	result, err := s.rc.HGetAll(s.ctx, pollResultsKey(roomId, pollId)).Result()
	return result, err
}

// GetPollTotalResponses returns the poll's total distinct respondents,
// 0 when either the poll or the counter is missing.
func (s *RedisService) GetPollTotalResponses(roomId, pollId string) (uint64, error) {
	result, err := s.rc.HGet(s.ctx, pollResultsKey(roomId, pollId), PollTotalRespField).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return 0, nil
	case err != nil:
		return 0, err
	}
	return parseUint64(result)
}

// GetPollRespondent returns the typed respondent stored for a voter,
// or nil when that user has not voted. Anonymous polls always return a
// respondent with empty option ids (participation only).
func (s *RedisService) GetPollRespondent(roomId, pollId, userId string) (*plugnmeet.PollRespondent, error) {
	result, err := s.rc.HGet(s.ctx, pollVotersKey(roomId, pollId), userId).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return nil, nil
	case err != nil:
		return nil, err
	case result == PollAnonymousMarker:
		return &plugnmeet.PollRespondent{UserId: userId}, nil
	}
	r := new(plugnmeet.PollRespondent)
	if err = protojson.Unmarshal([]byte(result), r); err != nil {
		return nil, err
	}
	return r, nil
}

// IsUserVotedPoll checks the voters hash membership (works for anonymous polls
// too, where only participation is stored).
func (s *RedisService) IsUserVotedPoll(roomId, pollId, userId string) (bool, error) {
	return s.rc.HExists(s.ctx, pollVotersKey(roomId, pollId), userId).Result()
}

// GetPollResponsesDetails builds the typed responses detail: aggregate counts
// from the results hash plus the stored respondents. Anonymous polls keep
// counts but their respondents are never exposed (only the marker is stored,
// keyed by user id — the list is skipped to avoid leaking even user ids).
func (s *RedisService) GetPollResponsesDetails(roomId, pollId string, info *plugnmeet.PollInfo) (*plugnmeet.PollResponsesDetails, error) {
	counters, err := s.GetPollResultsRaw(roomId, pollId)
	if err != nil {
		return nil, err
	}

	details := new(plugnmeet.PollResponsesDetails)
	details.TotalRespondents = countersU64(counters, PollTotalRespField)

	options := make([]*plugnmeet.PollResponsesResultOptions, 0, len(info.GetOptions()))
	for _, opt := range info.GetOptions() {
		options = append(options, &plugnmeet.PollResponsesResultOptions{
			Id:        uint64(opt.GetId()),
			VoteCount: countersU64(counters, pollOptionCountField(uint64(opt.GetId()))),
		})
	}
	details.Options = options

	if info.GetIsAnonymous() {
		return details, nil
	}

	voters, err := s.rc.HGetAll(s.ctx, pollVotersKey(roomId, pollId)).Result()
	if err != nil {
		return nil, err
	}
	respondents := make([]*plugnmeet.PollRespondent, 0, len(voters))
	for _, raw := range voters {
		if raw == PollAnonymousMarker {
			continue
		}
		r := new(plugnmeet.PollRespondent)
		if err = protojson.Unmarshal([]byte(raw), r); err != nil {
			return nil, err
		}
		respondents = append(respondents, r)
	}
	details.Respondents = respondents
	return details, nil
}
