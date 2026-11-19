package mcp

import (
	"context"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolSendChatMessage = "send_chat_message"

	maxChatMessageChars = 2000
	maxChatTargetChars  = 128
)

// ChatBackend delivers the agent's written messages to the AI Chat box. A blank
// toUserID marks a room-wide message; otherwise it is private to that user.
func registerChatTools(reg *Registry, deps ToolDependencies) {
	if deps.Chat == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolSendChatMessage,
			Description: "Send a written message to the AI Chat box: exact spellings, vocabulary lists, short " +
				"explanations, code/formulas, links, prompts to reply to, or answers to a participant's message. " +
				"Prefer the whiteboard for drawings and the shared notepad for long notes.",
			InputSchema: objectSchema(map[string]any{
				"markdown_text": stringProp("The message; markdown is supported.", maxChatMessageChars),
				"to_user_id": stringProp("Optional participant user_id for a private reply; leave empty for the "+
					"participant who last messaged you (or the whole room).", maxChatTargetChars),
			}, "markdown_text"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				MarkdownText string `json:"markdown_text"`
				ToUserID     string `json:"to_user_id"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			if strings.TrimSpace(in.MarkdownText) == "" {
				return errorResult(call, "markdown_text must not be empty"), nil
			}
			if err := deps.Chat.SendChatMessage(ctx, call.RoomID, strings.TrimSpace(in.ToUserID), in.MarkdownText); err != nil {
				return errorResult(call, "the message could not be sent to the AI Chat box: %s", err.Error()), nil
			}
			return resultJSON(call, map[string]any{"sent": true}), nil
		},
	})
}
