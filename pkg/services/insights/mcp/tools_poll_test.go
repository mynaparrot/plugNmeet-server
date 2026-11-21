package mcp

import (
	"context"
	"strings"
	"testing"
)

func newPollTestExecutor() (*Executor, *fakePoll) {
	polls := &fakePoll{}
	deps := testDeps()
	deps.Poll = polls
	return NewExecutor(ExecutorOptions{Registry: BuildRoomToolRegistry(fullFeatures(), deps)}), polls
}

func TestCreatePollTextAnswer(t *testing.T) {
	exec, polls := newPollTestExecutor()

	res := exec.Execute(context.Background(), ToolCall{
		CallID:    "call-1",
		RoomID:    "room-1",
		SessionID: "s1",
		ToolName:  ToolCreatePoll,
		// options are ignored on a free-text poll, so a model that sends both
		// still gets a text-answer poll
		ArgsJSON: `{"question":"What confused you?","options":["a","b"],"is_text_answer":true}`,
	})
	if res.IsError {
		t.Fatalf("create_poll failed: %s", res.OutputJSON)
	}
	if len(polls.textAnswers) != 1 || !polls.textAnswers[0] {
		t.Fatalf("is_text_answer not passed to the backend: %+v", polls.textAnswers)
	}
	if len(polls.options) != 1 || len(polls.options[0]) != 0 {
		t.Fatalf("a free-text poll must be created without options: %+v", polls.options)
	}
	for _, want := range []string{`"poll_id"`, `"is_quiz":false`, `"is_text_answer":true`, `"opened":true`} {
		if !strings.Contains(res.OutputJSON, want) {
			t.Fatalf("output %s does not contain %s", res.OutputJSON, want)
		}
	}
}

func TestCreatePollChoiceRequiresOptions(t *testing.T) {
	for _, args := range []string{`{"question":"Pick one"}`, `{"question":"Pick one","options":["only"]}`} {
		exec, polls := newPollTestExecutor()
		res := exec.Execute(context.Background(), ToolCall{
			CallID:    "call-1",
			RoomID:    "room-1",
			SessionID: "s1",
			ToolName:  ToolCreatePoll,
			ArgsJSON:  args,
		})
		if !res.IsError || !strings.Contains(res.OutputJSON, "is_text_answer=true") {
			t.Fatalf("args %s: expected the choice-poll validation error, got %s", args, res.OutputJSON)
		}
		if len(polls.questions) != 0 {
			t.Fatalf("args %s: an invalid choice poll must not reach the backend", args)
		}
	}
}

func TestCreatePollChoiceWithOptions(t *testing.T) {
	exec, polls := newPollTestExecutor()

	res := exec.Execute(context.Background(), ToolCall{
		CallID:    "call-1",
		RoomID:    "room-1",
		SessionID: "s1",
		ToolName:  ToolCreatePoll,
		ArgsJSON:  `{"question":"Past tense of go?","options":["goed","went"]}`,
	})
	if res.IsError {
		t.Fatalf("create_poll failed: %s", res.OutputJSON)
	}
	if len(polls.textAnswers) != 1 || polls.textAnswers[0] {
		t.Fatalf("a choice poll must not set is_text_answer: %+v", polls.textAnswers)
	}
	if len(polls.options) != 1 || len(polls.options[0]) != 2 || polls.options[0][1] != "went" {
		t.Fatalf("options not passed to the backend: %+v", polls.options)
	}
	if !strings.Contains(res.OutputJSON, `"is_text_answer":false`) {
		t.Fatalf("output %s must report is_text_answer=false", res.OutputJSON)
	}
}
