package models

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *PollModel) ListPolls(roomId string) ([]*plugnmeet.PollInfo, error) {
	var polls []*plugnmeet.PollInfo

	result, err := m.rs.GetPollsListByRoomId(roomId)
	if err != nil {
		m.logger.WithError(err).WithFields(logrus.Fields{
			"roomId": roomId,
			"method": "ListPolls",
		}).Errorln("failed to fetch polls list from redis")
		return nil, config.ErrPollGeneric
	}

	if result == nil || len(result) == 0 {
		// no polls
		return polls, err
	}

	for _, pi := range result {
		info := new(plugnmeet.PollInfo)
		err = protojson.Unmarshal([]byte(pi), info)
		if err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"roomId": roomId,
				"method": "ListPolls",
			}).Warnln("failed to unmarshal stored poll info")
			continue
		}

		// lazy expiry check so restart-lost timers still close expired polls
		m.maybeAutoCloseExpired(info)

		polls = append(polls, sanitizePollInfoForClient(info))
	}

	return polls, nil
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

	pi, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, nil, config.ErrPollGeneric
	}
	if pi == "" {
		return nil, nil, config.ErrPollNotFound
	}

	info := new(plugnmeet.PollInfo)
	if err = protojson.Unmarshal([]byte(pi), info); err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return nil, nil, config.ErrPollGeneric
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

	allRespondents, err := m.rs.GetPollAllRespondents(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll respondents")
		return nil, nil, config.ErrPollGeneric
	}

	var voted []uint64
	for i := 0; i < len(allRespondents); i++ {
		// format userId:option_id(:option_id...):name
		p := strings.Split(allRespondents[i], ":")
		if len(p) < 2 || p[0] != userId {
			continue
		}
		ids, err := config.SplitPollOptionIds(p[1])
		if err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"roomId": roomId,
				"pollId": pollId,
				"method": "UserSelectedOption",
			}).Errorln("failed to parse stored poll option ids")
			return nil, nil, config.ErrPollGeneric
		}
		voted = append(voted, ids...)
	}

	hasVoted := len(voted) > 0
	return voted, &hasVoted, nil
}

func (m *PollModel) GetPollResponsesDetails(roomId, pollId string) (map[string]string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "GetPollResponsesDetails",
	})

	// poll info is required to respect anonymity rules
	pi, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, config.ErrPollGeneric
	}
	if pi == "" {
		return nil, config.ErrPollNotFound
	}
	info := new(plugnmeet.PollInfo)
	if err = protojson.Unmarshal([]byte(pi), info); err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return nil, config.ErrPollGeneric
	}

	// Get the counters first
	result, err := m.rs.GetPollCountersByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll counters")
		return nil, config.ErrPollGeneric
	}
	if result == nil {
		result = make(map[string]string)
	}

	// anonymous polls: aggregates only, never per-user responses
	if info.IsAnonymous {
		if _, ok := result[redisservice.PollTotalRespField]; !ok {
			result[redisservice.PollTotalRespField] = "0"
		}
		return result, nil
	}

	// Now get the detailed list of respondents
	allRespondents, err := m.rs.GetPollAllRespondents(roomId, pollId)
	if err != nil {
		// Log the error but continue, as we might still have the counters
		m.logger.WithError(err).Warn("could not fetch all_respondents list")
	}

	// Marshal the list into a JSON string to match the original output format
	jsonRespondents, _ := json.Marshal(allRespondents)
	result["all_respondents"] = string(jsonRespondents)

	// Ensure total_resp is always present for backward compatibility.
	if _, ok := result[redisservice.PollTotalRespField]; !ok {
		result[redisservice.PollTotalRespField] = "0"
	}
	return result, nil
}

func (m *PollModel) GetResponsesResult(roomId, pollId string) (*plugnmeet.PollResponsesResult, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId": roomId,
		"pollId": pollId,
		"method": "GetResponsesResult",
	})

	pi, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll info")
		return nil, config.ErrPollGeneric
	}
	if pi == "" {
		return nil, config.ErrPollNotFound
	}

	info := new(plugnmeet.PollInfo)
	err = protojson.Unmarshal([]byte(pi), info)
	if err != nil {
		log.WithError(err).Errorln("failed to unmarshal stored poll info")
		return nil, config.ErrPollGeneric
	}
	if info.IsRunning {
		return nil, config.ErrPollWaitForClose
	}

	res := new(plugnmeet.PollResponsesResult)
	res.Question = info.Question

	result, err := m.rs.GetPollCountersByPollId(roomId, pollId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch poll counters")
		return nil, config.ErrPollGeneric
	}
	if result == nil {
		return nil, nil
	}

	var options []*plugnmeet.PollResponsesResultOptions
	var totalVotes uint64
	for _, opt := range info.Options {
		f := fmt.Sprintf("%d%s", opt.Id, redisservice.PollCountSuffix)
		i, _ := strconv.Atoi(result[f])
		totalVotes += uint64(i)
		rr := &plugnmeet.PollResponsesResultOptions{
			Id:        uint64(opt.Id),
			Text:      opt.Text,
			VoteCount: uint64(i),
			IsCorrect: opt.IsCorrect, // poll is closed here, so real values are revealed
		}
		options = append(options, rr)
	}

	res.Options = options
	i, _ := strconv.Atoi(result[redisservice.PollTotalRespField])
	res.TotalResponses = uint64(i)
	// total_votes = all selections; equals total_responses for single-choice polls
	res.TotalVotes = totalVotes

	return res, nil
}

func (m *PollModel) GetPollsStats(roomId string) (*plugnmeet.PollsStats, error) {
	res := &plugnmeet.PollsStats{
		TotalPolls:   0,
		TotalRunning: 0,
	}

	result, err := m.rs.GetPollsListByRoomId(roomId)
	if err != nil {
		m.logger.WithError(err).WithFields(logrus.Fields{
			"roomId": roomId,
			"method": "GetPollsStats",
		}).Errorln("failed to fetch polls list from redis")
		return nil, config.ErrPollGeneric
	}

	if result == nil || len(result) == 0 {
		// no polls
		return nil, nil
	}
	res.TotalPolls = uint64(len(result))

	for _, pi := range result {
		info := new(plugnmeet.PollInfo)
		err = protojson.Unmarshal([]byte(pi), info)
		if err != nil {
			m.logger.WithError(err).WithFields(logrus.Fields{
				"roomId": roomId,
				"method": "GetPollsStats",
			}).Warnln("failed to unmarshal stored poll info")
			continue
		}

		if info.IsRunning {
			res.TotalRunning += 1
		}
	}

	return res, nil
}
