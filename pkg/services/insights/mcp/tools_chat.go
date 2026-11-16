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
			Description: "Send a written message to the AI Chat box. Use it to share exact spellings, vocabulary lists, " +
				"short explanations, code/formulas, links, prompts for the participant to reply to in AI Chat, or written " +
				"replies when a participant messages you in AI Chat. Prefer the whiteboard for visual drawings and the " +
				"shared notepad for long structured notes.",
			InputSchema: objectSchema(map[string]any{
				"markdown_text": stringProp("The message to display in the AI Chat box; markdown is supported.", maxChatMessageChars),
				"to_user_id": stringProp("Optional target participant user_id for a private reply in their AI Chat box. "+
					"Leave empty to send to the participant who last messaged you in AI Chat (or to everyone in the room).", maxChatTargetChars),
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
