package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func TestBuildChatTools(t *testing.T) {
	params := buildChatTools([]*insights.MCPToolSpec{
		{
			Name:        "draft_quiz",
			Description: "Preview a quiz in the chat.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		nil,
		{Name: ""},
	})

	if len(params) != 1 {
		t.Fatalf("tools = %d, want the single named tool", len(params))
	}
	function := params[0].GetFunction()
	if function == nil || function.Name != "draft_quiz" {
		t.Fatalf("function = %+v, want draft_quiz", function)
	}
	if got := function.Description.Value; got != "Preview a quiz in the chat." {
		t.Errorf("description = %q", got)
	}

	raw, err := json.Marshal(params[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"type":"function"`, `"function"`, `"parameters"`, `"draft_quiz"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("serialized tool %s misses %s", raw, want)
		}
	}
}

func TestBuildChatToolsWithoutSpecs(t *testing.T) {
	if got := buildChatTools(nil); len(got) != 0 {
		t.Errorf("tools = %d, want none", len(got))
	}
}

func TestAssistantToolCallMessage(t *testing.T) {
	message := assistantToolCallMessage("let me check", []*streamedToolCall{
		{id: "call-1", name: "notepad_read", arguments: `{"mode":"full"}`},
	})

	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"role":"assistant"`,
		`"content":"let me check"`,
		`"tool_calls":[{"id":"call-1"`,
		`"name":"notepad_read"`,
		`"arguments":"{\"mode\":\"full\"}"`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("assistant message %s misses %s", raw, want)
		}
	}
}

func TestAssistantToolCallMessageWithoutContent(t *testing.T) {
	message := assistantToolCallMessage("", []*streamedToolCall{{id: "call-1", name: "poll_close"}})

	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), `"content"`) {
		t.Errorf("an empty content must be omitted, got %s", raw)
	}
}
