package google

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/genai"
)

// toGenaiContent converts our protobuf history to the genai library's format.
func toGenaiContent(history []*plugnmeet.InsightsAITextChatContent) []*genai.Content {
	var content []*genai.Content
	for _, h := range history {
		switch h.Role {
		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER:
			content = append(content, genai.NewContentFromText(h.Text, genai.RoleUser))
		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL:
			content = append(content, genai.NewContentFromText(h.Text, genai.RoleModel))
		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM:
			// System instructions are handled at the chat creation level,
			// but for history, we can treat it as a model turn.
			content = append(content, genai.NewContentFromText(h.Text, genai.RoleModel))
		}
	}
	return content
}
