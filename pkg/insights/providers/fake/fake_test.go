package fake

import (
	"context"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func drain(results <-chan *plugnmeet.InsightsAITextChatStreamResult) (text string, last bool) {
	for res := range results {
		text += res.GetText()
		last = last || res.GetIsLastChunk()
	}
	return text, last
}

func TestAITextChatStreamRecordsAndStreams(t *testing.T) {
	provider := NewProvider()
	provider.ChatChunks = []string{"hello ", "world"}

	stream, err := provider.AITextChatStream(context.Background(), "chat-model", []*plugnmeet.InsightsAITextChatContent{
		{Text: "hi"},
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	text, last := drain(stream)
	if text != "hello world" || !last {
		t.Fatalf("stream = (%q, %v), want the scripted text and a last chunk", text, last)
	}

	requests := provider.ChatRequests()
	if len(requests) != 1 || requests[0].Model != "chat-model" || len(requests[0].Tools) != 0 {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestAITextChatStreamWithToolsRunsScriptedCalls(t *testing.T) {
	provider := NewProvider()
	provider.ChatChunks = []string{"done"}
	provider.ChatToolCalls = []ScriptedToolCall{
		{Name: "notepad_read", ArgsJSON: "{}"},
		{Name: "draft_quiz", ArgsJSON: `{"question":"q"}`},
	}

	var executed []string
	stream, err := provider.AITextChatStreamWithTools(context.Background(), "chat-model",
		[]*plugnmeet.InsightsAITextChatContent{{Text: "hi"}},
		&insights.AITextChatToolRunner{
			Tools: []*insights.MCPToolSpec{{Name: "notepad_read"}, {Name: "draft_quiz"}},
			Execute: func(ctx context.Context, callID, name, argsJSON string) (string, bool) {
				executed = append(executed, name+":"+callID)
				return `{"ok":true}`, name == "draft_quiz"
			},
		})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	text, last := drain(stream)
	if text != "done" || !last {
		t.Fatalf("stream = (%q, %v)", text, last)
	}

	if len(executed) != 2 || executed[0] != "notepad_read:notepad_read-call" {
		t.Fatalf("executed = %v", executed)
	}
	executions := provider.ToolExecutions()
	if len(executions) != 2 || !executions[1].IsError || executions[1].Output != `{"ok":true}` {
		t.Fatalf("tool executions = %+v", executions)
	}
	requests := provider.ChatRequests()
	if len(requests) != 1 || len(requests[0].Tools) != 2 {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestAITextChatStreamError(t *testing.T) {
	provider := NewProvider()
	provider.ChatErr = ErrNotImplemented

	if _, err := provider.AITextChatStream(context.Background(), "", nil); err == nil {
		t.Error("the scripted error must be returned")
	}
	if _, err := provider.AITextChatStreamWithTools(context.Background(), "", nil, nil); err == nil {
		t.Error("the scripted error must be returned")
	}
}
