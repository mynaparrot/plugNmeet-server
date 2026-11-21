package models

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

type sentNote struct {
	text   string
	source string
}

// recordingAgentSender records the notes pushed into the live session.
type recordingAgentSender struct {
	notes []sentNote
}

func (r *recordingAgentSender) SendProactiveContext(text, source string) error {
	r.notes = append(r.notes, sentNote{text: text, source: source})
	return nil
}

func (r *recordingAgentSender) Stop(string) {}

func pollHookTestModel(t *testing.T) (*AIAgentModel, *recordingAgentSender) {
	t.Helper()

	model := newAIAgentTestModel(newAIAgentTestRedis(t))
	sender := new(recordingAgentSender)
	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return sender, true
	}

	return model, sender
}

func createTestPoll(t *testing.T, model *AIAgentModel, info *plugnmeet.PollInfo) {
	t.Helper()

	info.IsRunning = true
	info.Created = time.Now().Unix()
	info.ExpiresAt = time.Now().Unix() + 600
	if err := model.redisService.CreateRoomPoll(info.RoomId, info); err != nil {
		t.Fatalf("create poll: %v", err)
	}
}

func answerTestPoll(t *testing.T, model *AIAgentModel, req *plugnmeet.SubmitPollResponseReq) {
	t.Helper()

	if err := model.redisService.AddPollResponse(req); err != nil {
		t.Fatalf("add poll response: %v", err)
	}
}

func newChoicePoll(roomID, pollID, question string) *plugnmeet.PollInfo {
	return &plugnmeet.PollInfo{
		Id:       pollID,
		RoomId:   roomID,
		Question: question,
		Options:  []*plugnmeet.CreatePollOptions{{Id: 1, Text: "3"}, {Id: 2, Text: "4"}},
	}
}

func TestOnPollResponseSubmittedSendsAProactiveContext(t *testing.T) {
	model, sender := pollHookTestModel(t)
	createTestPoll(t, model, newChoicePoll("room-1", "poll-1", "What is 2+2?"))
	answerTestPoll(t, model, &plugnmeet.SubmitPollResponseReq{
		RoomId: "room-1", PollId: "poll-1", UserId: "u1", Name: "Sam",
		SelectedOptions: []uint64{2},
	})

	model.OnPollResponseSubmitted(context.Background(), "room-1", "poll-1", "u1", "", 2)

	if len(sender.notes) != 1 {
		t.Fatalf("sent notes = %d, want 1", len(sender.notes))
	}
	note := sender.notes[0]
	if note.source != "poll" {
		t.Errorf("source = %q, want poll", note.source)
	}
	for _, want := range []string{"Sam", "option 2", `"4"`, "What is 2+2?"} {
		if !strings.Contains(note.text, want) {
			t.Errorf("note %q does not mention %q", note.text, want)
		}
	}
}

func TestOnPollResponseSubmittedDescribesATextAnswer(t *testing.T) {
	model, sender := pollHookTestModel(t)
	newTextPoll := newChoicePoll("room-1", "poll-2", "Explain your answer")
	newTextPoll.Options = nil
	newTextPoll.IsTextAnswer = true
	createTestPoll(t, model, newTextPoll)
	answerTestPoll(t, model, &plugnmeet.SubmitPollResponseReq{
		RoomId: "room-1", PollId: "poll-2", UserId: "u2", Name: "Lee",
		TextAnswer: "2 plus 2 equals 4",
	})

	model.OnPollResponseSubmitted(context.Background(), "room-1", "poll-2", "u2", "", 0)

	if len(sender.notes) != 1 {
		t.Fatalf("sent notes = %d, want 1", len(sender.notes))
	}
	note := sender.notes[0]
	if note.source != "poll" {
		t.Errorf("source = %q, want poll", note.source)
	}
	if !strings.Contains(note.text, `"2 plus 2 equals 4"`) {
		t.Errorf("note %q does not quote the answer", note.text)
	}
}

func TestOnPollResponseSubmittedSkipsWhatItCannotDescribe(t *testing.T) {
	model, sender := pollHookTestModel(t)
	createTestPoll(t, model, newChoicePoll("room-1", "poll-1", "What is 2+2?"))
	answerTestPoll(t, model, &plugnmeet.SubmitPollResponseReq{
		RoomId: "room-1", PollId: "poll-1", UserId: "u1", Name: "Sam",
		SelectedOptions: []uint64{2},
	})

	// the agent's own synthetic participant never answers a poll
	model.OnPollResponseSubmitted(context.Background(), "room-1", "poll-1", config.AgentUserUserIdPrefix+"1", "", 2)
	// an unknown poll has no question to report
	model.OnPollResponseSubmitted(context.Background(), "room-1", "poll-404", "u1", "", 2)
	if len(sender.notes) != 0 {
		t.Fatalf("sent notes = %d, want none", len(sender.notes))
	}

	// without a hosted agent there is no session to push the note into
	model.activeAgentLookup = func(string) (aiAgentHandle, bool) {
		return nil, false
	}
	model.OnPollResponseSubmitted(context.Background(), "room-1", "poll-1", "u1", "", 2)
	if len(sender.notes) != 0 {
		t.Fatalf("sent notes = %d, want none without a hosted agent", len(sender.notes))
	}
}

func TestBuildPollNoteDropsAnAnswerThatIsNoOption(t *testing.T) {
	model, _ := pollHookTestModel(t)
	createTestPoll(t, model, newChoicePoll("room-1", "poll-1", "What is 2+2?"))

	// the reported answer belongs to no option of this poll
	if note := model.buildPollNote(context.Background(), "room-1", "poll-1", "u9", "", 9); note != "" {
		t.Fatalf("buildPollNote() = %q, want an empty note", note)
	}
}
