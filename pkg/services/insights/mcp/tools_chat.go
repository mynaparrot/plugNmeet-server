package mcp

import (
	"context"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	// ToolSendAIChatMessage writes into the caller's AI Chat box.
	ToolSendAIChatMessage = "send_ai_chat_message"
	// ToolPublishChatMessage posts into the room's public/private chat panel
	// from the caller's browser instead of the AI Chat box.
	ToolPublishChatMessage = "publish_chat_message"

	maxChatMessageChars = 2000
	maxChatTargetChars  = 128
)

// registerChatTools adds the chat tools. send_ai_chat_message needs the AI Chat
// box backend; publish_chat_message runs in the caller's browser, so it is
// registered even when that backend is absent.
func registerChatTools(reg *Registry, deps ToolDependencies) {
	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolPublishChatMessage,
			Description: "Publish a message to the room's public chat panel (or privately to one participant) " +
				"from the caller's client so everyone in the room sees it in the regular chat. " +
				"Format it for the chat bubble: use clean markdown or basic HTML, like **bold**/<strong>, " +
				"bullet lists with '- ', numbered lists with '1. ', and a blank line between sections, so " +
				"summaries and poll results with several questions stay clearly separated and easy to scan.",
			InputSchema: objectSchema(map[string]any{
				"message": stringProp("The message text or markdown to publish in the room chat. Use markdown "+
					"or basic HTML (bold, lists, blank lines between sections) so a multi-part message stays "+
					"readable.", maxChatMessageChars),
				"to_user_id": stringProp("Optional participant user_id for a private chat message; leave empty "+
					"to publish to public chat.", maxChatTargetChars),
			}, "message"),
		},
	})

	if deps.Chat == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolSendAIChatMessage,
			Description: "Send a written message to the AI Chat box: exact spellings, vocabulary lists, short " +
				"explanations, code/formulas, links, prompts to reply to, or answers to a participant's message. " +
				"Use publish_chat_message when the whole room should see it in the regular chat panel. " +
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
