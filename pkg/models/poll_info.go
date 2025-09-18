package models

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"google.golang.org/protobuf/encoding/protojson"
)

func (m *PollModel) ListPolls(roomId string) ([]*plugnmeet.PollInfo, error) {
	var polls []*plugnmeet.PollInfo

	result, err := m.rs.GetPollsListByRoomId(roomId)
	if err != nil {
		return nil, err
	}

	if result == nil || len(result) == 0 {
		// no polls
		return polls, err
	}

	for _, pi := range result {
		info := new(plugnmeet.PollInfo)
		err = protojson.Unmarshal([]byte(pi), info)
		if err != nil {
			continue
		}

		polls = append(polls, info)
	}

	return polls, nil
}

func (m *PollModel) UserSelectedOption(roomId, pollId, userId string) (uint64, error) {
	allRespondents, err := m.rs.GetPollAllRespondents(roomId, pollId)
	if err != nil {
		return 0, err
	}

	for i := 0; i < len(allRespondents); i++ {
		// format userId:option_id:name
		p := strings.Split(allRespondents[i], ":")
		if p[0] == userId {
			voted, err := strconv.ParseUint(p[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return voted, nil
		}
	}

	// user did not vote
	return 0, nil
}

func (m *PollModel) GetPollResponsesDetails(roomId, pollId string) (map[string]string, error) {
	// Get the counters first
	result, err := m.rs.GetPollCountersByPollId(roomId, pollId)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = make(map[string]string)
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
	pi, err := m.rs.GetPollInfoByPollId(roomId, pollId)
	if err != nil {
		return nil, err
	}

	info := new(plugnmeet.PollInfo)
	err = protojson.Unmarshal([]byte(pi), info)
	if err != nil {
		return nil, err
	}
	if info.IsRunning {
		return nil, errors.New("need to wait until poll close")
	}

	res := new(plugnmeet.PollResponsesResult)
	res.Question = info.Question

	result, err := m.rs.GetPollCountersByPollId(roomId, pollId)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}

	var options []*plugnmeet.PollResponsesResultOptions
	for _, opt := range info.Options {
		f := fmt.Sprintf("%d%s", opt.Id, redisservice.PollCountSuffix)
		i, _ := strconv.Atoi(result[f])
		rr := &plugnmeet.PollResponsesResultOptions{
			Id:        uint64(opt.Id),
			Text:      opt.Text,
			VoteCount: uint64(i),
		}
		options = append(options, rr)
	}

	res.Options = options
	i, _ := strconv.Atoi(result[redisservice.PollTotalRespField])
	res.TotalResponses = uint64(i)

	return res, nil
}

func (m *PollModel) GetPollsStats(roomId string) (*plugnmeet.PollsStats, error) {
	res := &plugnmeet.PollsStats{
		TotalPolls:   0,
		TotalRunning: 0,
	}

	result, err := m.rs.GetPollsListByRoomId(roomId)
	if err != nil {
		return nil, err
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
			continue
		}

		if info.IsRunning {
			res.TotalRunning += 1
		}
	}

	return res, nil
}
