package models

import (
	"fmt"
	"strconv"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

func (m *PollModel) ListPolls(roomId string) ([]*plugnmeet.PollInfo, error) {
	polls, err := m.rs.GetRoomPolls(roomId)
	if err != nil {
		m.logger.WithError(err).WithFields(logrus.Fields{
			"roomId": roomId,
			"method": "ListPolls",
		}).Errorln("failed to fetch polls list from redis")
		return nil, config.ErrPollGeneric
	}

	out := make([]*plugnmeet.PollInfo, 0, len(polls))
	for _, info := range polls {
		// lazy expiry check so restart-lost timers still close expired polls
		m.maybeAutoCloseExpired(info)
		out = append(out, sanitizePollInfoForClient(info))
	}

	return out, nil
}

// sanitizePollInfoForClient strips correct-answer flags from any PollInfo leaving
// the server while the poll is running; real values are revealed once it is closed.
func sanitizePollInfoForClient(info *plugnmeet.PollInfo) *plugnmeet.PollInfo {
	if info == nil || !info.IsRunning {
		return info
	}
	for _, opt := range info.Options {
		opt.IsCorrect = false
	}
	return info
}

func (m *PollModel) UserSelectedOption(roomId, pollId, userId string) ([]uint64, *bool, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "UserSelectedOption",
	})

	info, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, nil, config.ErrPollGeneric
	}
	if info == nil {
		return nil, nil, config.ErrPollNotFound
	}

	// anonymous polls: participation only, never the choice
	if info.IsAnonymous {
		voted, err := m.rs.IsUserVotedPoll(roomId, pollId, userId)
		if err != nil {
			log.WithError(err).Errorln("failed to check voted users set")
			return nil, nil, config.ErrPollGeneric
		}
		return nil, &voted, nil
	}

	respondent, err := m.rs.GetPollRespondent(roomId, pollId, userId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll respondent")
		return nil, nil, config.ErrPollGeneric
	}
	if respondent == nil {
		return nil, new(bool), nil
	}

	hasVoted := true
	return respondent.OptionIds, &hasVoted, nil
}

func (m *PollModel) GetPollResponsesDetails(roomId, pollId string) (*plugnmeet.PollResponsesDetails, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "GetPollResponsesDetails",
	})

	// poll info is required to respect anonymity rules
	info, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, config.ErrPollGeneric
	}
	if info == nil {
		return nil, config.ErrPollNotFound
	}

	details, err := m.rs.GetPollResponsesDetails(roomId, pollId, info)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll responses details")
		return nil, config.ErrPollGeneric
	}
	return details, nil
}

func (m *PollModel) GetResponsesResult(roomId, pollId string) (*plugnmeet.PollResponsesResult, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "GetResponsesResult",
	})

	info, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, config.ErrPollGeneric
	}
	if info == nil {
		return nil, config.ErrPollNotFound
	}
	if info.IsRunning {
		return nil, config.ErrPollWaitForClose
	}

	counters, err := m.rs.GetPollResultsRaw(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll counters")
		return nil, config.ErrPollGeneric
	}

	res := new(plugnmeet.PollResponsesResult)
	res.Question = info.Question

	options := make([]*plugnmeet.PollResponsesResultOptions, 0, len(info.GetOptions()))
	var totalVotes uint64
	for _, opt := range info.GetOptions() {
		count := countersU64(counters, fmt.Sprintf("%d%s", opt.Id, redisservice.PollCountSuffix))
		totalVotes += count
		options = append(options, &plugnmeet.PollResponsesResultOptions{
			Id:        uint64(opt.Id),
			Text:      opt.Text,
			VoteCount: count,
			IsCorrect: opt.IsCorrect, // poll is closed here, so real values are revealed
		})
	}
	res.Options = options
	res.TotalResponses = countersU64(counters, redisservice.PollTotalRespField)
	// total_votes = all selections; equals total_responses for single-choice polls
	res.TotalVotes = totalVotes

	return res, nil
}

func (m *PollModel) GetPollsStats(roomId string) (*plugnmeet.PollsStats, error) {
	polls, err := m.rs.GetRoomPolls(roomId)
	if err != nil {
		m.logger.WithError(err).WithFields(logrus.Fields{
			"roomId": roomId,
			"method": "GetPollsStats",
		}).Errorln("failed to fetch polls list from redis")
		return nil, config.ErrPollGeneric
	}
	if len(polls) == 0 {
		// no polls
		return nil, nil
	}

	res := &plugnmeet.PollsStats{TotalPolls: uint64(len(polls))}
	for _, info := range polls {
		m.maybeAutoCloseExpired(info)
		if info.IsRunning {
			res.TotalRunning += 1
		}
	}

	return res, nil
}

// countersU64 reads a uint counter from the stored results map.
func countersU64(m map[string]string, field string) uint64 {
	v, _ := strconv.ParseUint(m[field], 10, 64)
	return v
}
