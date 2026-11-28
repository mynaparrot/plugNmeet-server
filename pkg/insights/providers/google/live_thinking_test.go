package google

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"google.golang.org/genai"
)

// translateTurn replays a whole turn through one shared transcript state.
func translateTurn(msgs ...*genai.LiveServerMessage) []insights.RealtimeEvent {
	var tr liveTranscripts
	var events []insights.RealtimeEvent
	for _, msg := range msgs {
		events = append(events, translateServerMessageWithState(msg, &tr)...)
	}
	return events
}

func assertEventTypes(t *testing.T, events []insights.RealtimeEvent, want ...insights.RealtimeEventType) {
	t.Helper()
	var got []insights.RealtimeEventType
	for _, ev := range events {
		got = append(got, ev.Type)
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: expected %s, got %s (%v)", i, want[i], got[i], got)
		}
	}
}

func serverContent(sc *genai.LiveServerContent) *genai.LiveServerMessage {
	return &genai.LiveServerMessage{ServerContent: sc}
}

// The participant is told the model is composing as soon as VAD closes the
// speech segment, long before the first audio frame arrives.
func TestInputTranscriptionMarksThinking(t *testing.T) {
	for _, finished := range []bool{false, true} {
		events := translateTurn(serverContent(&genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "what is a fraction", Finished: finished},
		}))

		assertEventTypes(t, events, insights.RealtimeEventTranscriptUser, insights.RealtimeEventThinking)
	}
}

// The whole turn reports THINKING once: the fragments after it, the flushed
// utterance and the audio that answers it must not repeat it.
func TestThinkingIsEmittedOncePerTurn(t *testing.T) {
	events := translateTurn(
		serverContent(&genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "what is "},
		}),
		serverContent(&genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "a fraction", Finished: true},
		}),
		serverContent(&genai.LiveServerContent{
			ModelTurn: &genai.Content{Parts: []*genai.Part{{InlineData: &genai.Blob{Data: []byte{1, 2}}}}},
		}),
		serverContent(&genai.LiveServerContent{
			OutputTranscription: &genai.Transcription{Text: "a part"},
		}),
	)

	assertEventTypes(t, events,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventThinking,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventAudioDelta,
		insights.RealtimeEventTranscriptAgent,
	)
}

// Gemini streams its reasoning as text before the audio it produces.
func TestModelTurnTextPartMarksThinking(t *testing.T) {
	for _, part := range []*genai.Part{
		{Thought: true, Text: "the participant asked about halves"},
		{Text: "a fraction is"},
	} {
		events := translateTurn(
			serverContent(&genai.LiveServerContent{
				ModelTurn: &genai.Content{Parts: []*genai.Part{part, {InlineData: &genai.Blob{Data: []byte{1}}}}},
			}),
		)

		assertEventTypes(t, events, insights.RealtimeEventThinking, insights.RealtimeEventAudioDelta)
	}
}

// Proactive audio lets the model stay silent: the pending utterance is closed
// and the turn ends, so the agent leaves THINKING instead of timing out.
func TestWaitingForInputEndsTurn(t *testing.T) {
	events := translateTurn(
		serverContent(&genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "just thinking out loud"},
		}),
		serverContent(&genai.LiveServerContent{WaitingForInput: true}),
		serverContent(&genai.LiveServerContent{
			InputTranscription: &genai.Transcription{Text: "never mind"},
		}),
	)

	assertEventTypes(t, events,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventThinking,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventTurnComplete,
		insights.RealtimeEventTranscriptUser,
		insights.RealtimeEventThinking,
	)
	if ev := events[2]; !ev.Final || ev.Text != "just thinking out loud" {
		t.Fatalf("the pending utterance must be flushed as final: %+v", ev)
	}
}

// A message that only closes the turn must not report thinking again.
func TestTranslateServerMessageNoThinkingWithoutPendingUtterance(t *testing.T) {
	msg := &genai.LiveServerMessage{
		ServerContent: &genai.LiveServerContent{TurnComplete: true},
	}

	for _, ev := range translateServerMessage(msg) {
		if ev.Type == insights.RealtimeEventThinking {
			t.Fatalf("a turn complete without speech must not mark thinking: %+v", ev)
		}
	}
}
