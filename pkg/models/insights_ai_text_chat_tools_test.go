package models

import (
	"context"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/redis/go-redis/v9"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/fake"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
)

func newInsightsTestModel(rc redis.UniversalClient) *InsightsModel {
	aiAgent := newAIAgentTestModel(rc)

	return &InsightsModel{
		ctx:          context.Background(),
		redisService: aiAgent.redisService,
		logger:       aiAgent.logger,
	}
}

// chatOnlyProvider hides the tool support of a provider, so the fallback path of
// textChatStream can be exercised.
type chatOnlyProvider struct {
	insights.Provider
}

func newPromptHistory() []*plugnmeet.InsightsAITextChatContent {
	return []*plugnmeet.InsightsAITextChatContent{
		{
			Role: plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER,
			Text: "prepare a quiz about the lesson",
		},
	}
}

func drainChatStream(t *testing.T, stream <-chan *plugnmeet.InsightsAITextChatStreamResult) string {
	t.Helper()

	var text strings.Builder
	last := false
	for res := range stream {
		text.WriteString(res.GetText())
		last = last || res.GetIsLastChunk()
	}
	if !last {
		t.Error("the stream must end with a last chunk")
	}

	return text.String()
}

func TestAITextChatAttachesToolsOnlyForAdmin(t *testing.T) {
	model := &InsightsModel{aiAgentModel: &AIAgentModel{}}
	runner := &insights.AITextChatToolRunner{
		Tools: []*insights.MCPToolSpec{{Name: "draft_quiz"}},
		Execute: func(ctx context.Context, callID, name, argsJSON string) (string, bool) {
			return `{"ok":true}`, false
		},
	}

	cases := []struct {
		name        string
		requestFrom plugnmeet.InsightsAIRequestSource
		ephemeral   bool
		userInfo    *plugnmeet.NatsKvUserInfo
		wantTools   bool
	}{
		{
			name:        "admin chat",
			requestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT,
			userInfo:    &plugnmeet.NatsKvUserInfo{UserId: "admin-1", IsAdmin: true},
			wantTools:   true,
		},
		{
			name:        "non admin chat",
			requestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT,
			userInfo:    &plugnmeet.NatsKvUserInfo{UserId: "user-1", IsAdmin: false},
		},
		{
			name:        "unknown user",
			requestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT,
		},
		{
			name:        "admin notepad is ephemeral",
			requestFrom: plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_NOTEPAD,
			ephemeral:   true,
			userInfo:    &plugnmeet.NatsKvUserInfo{UserId: "admin-1", IsAdmin: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tools *insights.AITextChatToolRunner
			if model.shouldAttachAdminTools(tc.requestFrom, tc.ephemeral, tc.userInfo) {
				tools = runner
			}

			history := newPromptHistory()
			provider := fake.NewProvider()
			provider.ChatChunks = []string{"here you go"}

			stream, err := textChatStream(context.Background(), provider, "chat-model", history, tools)
			if err != nil {
				t.Fatalf("stream failed: %v", err)
			}
			if got := drainChatStream(t, stream); got != "here you go" {
				t.Errorf("streamed text = %q", got)
			}

			requests := provider.ChatRequests()
			if len(requests) != 1 {
				t.Fatalf("chat requests = %d, want 1", len(requests))
			}
			request := requests[0]
			if got := len(request.Tools) > 0; got != tc.wantTools {
				t.Fatalf("tools attached = %v, want %v (tools %+v)", got, tc.wantTools, request.Tools)
			}
			if !tc.wantTools {
				if got := request.History[0].GetText(); got != history[0].GetText() {
					t.Errorf("a request without tools must keep the stored history, first item = %q", got)
				}
				return
			}

			// The tool instructions travel to the provider only.
			if got := request.History[0].GetRole(); got != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM {
				t.Errorf("first provider message role = %s, want system", got)
			}
			prompt := request.History[0].GetText()
			for _, want := range []string{"draft_quiz", "publish_quiz", "approval", "Speech-to-Text", "\"T\" icon", "spoken language"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("the admin tool instructions must mention %q", want)
				}
			}
			if got := request.History[len(request.History)-1].GetText(); got != history[0].GetText() {
				t.Errorf("the user prompt must stay last, got %q", got)
			}
			if got := history[0].GetRole(); got != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER {
				t.Error("the stored history must not be mutated")
			}
			if got := history[0].GetText(); got != "prepare a quiz about the lesson" {
				t.Errorf("the stored prompt changed to %q", got)
			}
		})
	}
}

func TestAITextChatFallsBackWhenTheProviderHasNoTools(t *testing.T) {
	history := newPromptHistory()
	provider := fake.NewProvider()
	provider.ChatChunks = []string{"plain answer"}

	// The wrapper hides AITextChatStreamWithTools.
	stream, err := textChatStream(context.Background(), chatOnlyProvider{Provider: provider}, "chat-model", history, &insights.AITextChatToolRunner{
		Tools:   []*insights.MCPToolSpec{{Name: "draft_quiz"}},
		Execute: func(context.Context, string, string, string) (string, bool) { return "{}", false },
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if got := drainChatStream(t, stream); got != "plain answer" {
		t.Errorf("streamed text = %q", got)
	}

	requests := provider.ChatRequests()
	if len(requests) != 1 || len(requests[0].Tools) != 0 {
		t.Fatalf("the plain stream must be used, requests = %+v", requests)
	}
	if got := requests[0].History[0].GetRole(); got != plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER {
		t.Error("the fallback must not add the tool instructions")
	}
}

func TestAITextChatToolRunnerExecutesTools(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	aiAgent := newAIAgentTestModel(rc)
	model := newInsightsTestModel(rc)
	model.aiAgentModel = aiAgent

	runner := &insights.AITextChatToolRunner{
		Tools: []*insights.MCPToolSpec{{Name: mcp.ToolNotepadRead}},
		Execute: func(ctx context.Context, callID, name, argsJSON string) (string, bool) {
			return `{"ok":true}`, false
		},
	}

	provider := fake.NewProvider()
	provider.ChatToolCalls = []fake.ScriptedToolCall{{Name: mcp.ToolNotepadRead, ArgsJSON: "{}"}}
	provider.ChatChunks = []string{"done"}

	stream, err := textChatStream(context.Background(), provider, "chat-model", newPromptHistory(), runner)
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	_ = drainChatStream(t, stream)

	executions := provider.ToolExecutions()
	if len(executions) != 1 || executions[0].Name != mcp.ToolNotepadRead || executions[0].IsError {
		t.Fatalf("tool executions = %+v", executions)
	}
}

func TestShouldAttachAdminToolsNeedsTheAgentModel(t *testing.T) {
	userInfo := &plugnmeet.NatsKvUserInfo{UserId: "admin-1", IsAdmin: true}
	requestFrom := plugnmeet.InsightsAIRequestSource_INSIGHTS_AI_REQUEST_SOURCE_CHAT

	if (&InsightsModel{}).shouldAttachAdminTools(requestFrom, false, userInfo) {
		t.Error("an unwired model must not attach tools")
	}
	if !(&InsightsModel{aiAgentModel: &AIAgentModel{}}).shouldAttachAdminTools(requestFrom, false, userInfo) {
		t.Error("an admin chat request must attach tools")
	}
}
