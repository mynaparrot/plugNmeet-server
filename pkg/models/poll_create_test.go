package models

import (
	"errors"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func TestValidateCreatePollReqTextAnswer(t *testing.T) {
	req := &plugnmeet.CreatePollReq{
		Question:     "  Your feedback?  ",
		IsTextAnswer: true,
		IsMultiple:   true,
		IsQuiz:       true,
		Options:      []*plugnmeet.CreatePollOptions{{Id: 1, Text: "only one"}},
	}
	if err := validateCreatePollReq(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.Question != "Your feedback?" {
		t.Fatalf("question not trimmed: %q", req.Question)
	}
	if req.IsMultiple || req.IsQuiz || req.Options != nil {
		t.Fatalf("text poll must drop options/quiz/multi-select: %+v", req)
	}

	req.Question = "   "
	if err := validateCreatePollReq(req); !errors.Is(err, config.ErrPollQuestionRequired) {
		t.Fatalf("expected question-required, got %v", err)
	}

	req.Question = "q"
	req.Duration = config.MaxPollDurationSeconds + 1
	if err := validateCreatePollReq(req); !errors.Is(err, config.ErrPollDurationCap) {
		t.Fatalf("expected duration-cap, got %v", err)
	}
}

func TestValidateCreatePollReqChoiceUnchanged(t *testing.T) {
	req := &plugnmeet.CreatePollReq{
		Question: "q",
		IsQuiz:   true,
		Options:  []*plugnmeet.CreatePollOptions{{Id: 1, Text: "a"}},
	}
	if err := validateCreatePollReq(req); !errors.Is(err, config.ErrPollMinOptions) {
		t.Fatalf("expected min-options, got %v", err)
	}

	req.Options = append(req.Options, &plugnmeet.CreatePollOptions{Id: 2, Text: "b"})
	if err := validateCreatePollReq(req); !errors.Is(err, config.ErrPollQuizNeedsCorrect) {
		t.Fatalf("expected quiz-needs-correct, got %v", err)
	}
}

func TestNewPollCreateReqTextAnswer(t *testing.T) {
	req := newPollCreateReq("room-1", "Your feedback?", nil, true)
	if !req.GetIsTextAnswer() {
		t.Fatal("a free-text poll must set IsTextAnswer")
	}
	if req.GetOptions() != nil {
		t.Fatalf("a free-text poll must not carry options: %+v", req.GetOptions())
	}
	if req.GetIsQuiz() || req.GetIsMultiple() {
		t.Fatalf("a free-text poll must not be a quiz or multi-select: %+v", req)
	}
	if req.GetQuestion() != "Your feedback?" || req.GetRoomId() != "room-1" || req.GetUserId() == "" {
		t.Fatalf("request not filled: %+v", req)
	}
}

func TestNewPollCreateReqChoice(t *testing.T) {
	req := newPollCreateReq("room-1", "Past tense of go?", []string{"goed", "went"}, false)
	if req.GetIsTextAnswer() {
		t.Fatal("a choice poll must not set IsTextAnswer")
	}
	opts := req.GetOptions()
	if len(opts) != 2 || opts[0].GetId() != 1 || opts[0].GetText() != "goed" || opts[1].GetId() != 2 || opts[1].GetText() != "went" {
		t.Fatalf("options not mapped in order: %+v", opts)
	}
	if err := validateCreatePollReq(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNormalizeTextAnswer(t *testing.T) {
	req := &plugnmeet.SubmitPollResponseReq{TextAnswer: "   ", SelectedOptions: []uint64{1}}
	if err := normalizeTextAnswer(req); !errors.Is(err, config.ErrPollTextAnswerRequired) {
		t.Fatalf("expected text-answer-required, got %v", err)
	}

	req.TextAnswer = strings.Repeat("ü", config.MaxPollTextAnswerLength+1)
	if err := normalizeTextAnswer(req); !errors.Is(err, config.ErrPollTextAnswerTooLong) {
		t.Fatalf("expected text-answer-too-long, got %v", err)
	}

	// exactly at the rune limit is accepted and option ids are dropped
	req.TextAnswer = "  " + strings.Repeat("ü", config.MaxPollTextAnswerLength) + "  "
	if err := normalizeTextAnswer(req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if req.TextAnswer != strings.Repeat("ü", config.MaxPollTextAnswerLength) {
		t.Fatalf("text answer not trimmed: %q", req.TextAnswer)
	}
	if req.SelectedOptions != nil {
		t.Fatalf("selected options must be dropped: %v", req.SelectedOptions)
	}
}
