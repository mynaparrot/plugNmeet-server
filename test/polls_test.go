package test

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"net/http"
	"testing"
)

func test_CreatePoll(t *testing.T, token string) {
	var opts []*plugnmeet.CreatePollOptions
	opts = append(opts, &plugnmeet.CreatePollOptions{
		Id:   01,
		Text: "option 1",
	})
	opts = append(opts, &plugnmeet.CreatePollOptions{
		Id:   02,
		Text: "option 3",
	})

	b := new(plugnmeet.CreatePollReq)
	b.Question = "Test poll"
	b.Options = opts

	t.Run("CreatePoll", func(t *testing.T) {
		req := prepareStringWithTokenReq(token, http.MethodPost, "/api/polls/create", b)
		performCommonProtoReq(t, req, true)
	})
}

func test_ListPolls(t *testing.T, token string) {
	t.Run("ListPolls", func(t *testing.T) {
		req := prepareStringWithTokenReq(token, http.MethodGet, "/api/polls/listPolls", nil)
		performCommonProtoReq(t, req, true)
	})
}
