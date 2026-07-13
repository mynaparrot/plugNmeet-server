package openai

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/openai/openai-go/v3"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// chatStream handles the real-time streaming chat with OpenAI SDK.
func chatStream(ctx context.Context, client sdk.Client, service *config.ServiceConfig, model string, history []*plugnmeet.InsightsAITextChatContent, logger *logrus.Entry) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult)
	streamId := uuid.NewString()

	if model == "" {
		model = service.GetOptionsString("chat_model", sdk.ChatModelGPT5_4)
	}

	messages := toOpenAIMessageParams(history)

	go func() {
		defer close(resultChan)

		stream := client.Chat.Completions.NewStreaming(ctx, sdk.ChatCompletionNewParams{
			Model:    sdk.ChatModel(model),
			Messages: messages,
		})

		for stream.Next() {
			evt := stream.Current()

			if len(evt.Choices) == 0 {
				continue
			}

			content := evt.Choices[0].Delta.Content
			if content == "" {
				continue
			}

			resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
				Id:        streamId,
				Text:      content,
				CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
			}
		}

		if err := stream.Err(); err != nil {
			logger.WithError(err).Error("failed to execute openai chat stream request")
			return
		}

		// Signal the end of the stream.
		resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
			Id:          streamId,
			IsLastChunk: true,
			CreatedAt:   fmt.Sprintf("%d", time.Now().UnixMilli()),
		}
	}()

	return resultChan, nil
}

// summarize uses the non-streaming SDK call to get a summary of a conversation.
func summarize(ctx context.Context, client sdk.Client, service *config.ServiceConfig, model string, history []*plugnmeet.InsightsAITextChatContent) (summaryText string, promptTokens uint32, completionTokens uint32, err error) {
	if model == "" {
		model = service.GetOptionsString("summarize_model", sdk.ChatModelGPT5_4Mini)
	}

	messages := toOpenAIMessageParams(history)
	messages = append(messages, sdk.UserMessage(
		"Summarize the preceding conversation in a concise paragraph.",
	))

	resp, err := client.Chat.Completions.New(ctx, sdk.ChatCompletionNewParams{
		Model:    model,
		Messages: messages,
	})
	if err != nil {
		return "", 0, 0, fmt.Errorf("failed to execute summarize request: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", 0, 0, fmt.Errorf("no summary content found in response")
	}

	return resp.Choices[0].Message.Content,
		uint32(resp.Usage.PromptTokens),
		uint32(resp.Usage.CompletionTokens),
		nil
}

func toOpenAIMessageParams(history []*plugnmeet.InsightsAITextChatContent) []sdk.ChatCompletionMessageParamUnion {
	messages := make([]sdk.ChatCompletionMessageParamUnion, 0, len(history))

	for _, item := range history {
		if item == nil || item.Text == "" {
			continue
		}

		switch item.Role {
		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM:
			messages = append(messages, sdk.SystemMessage(item.Text))

		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER:
			messages = append(messages, sdk.UserMessage(item.Text))

		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL:
			messages = append(messages, sdk.AssistantMessage(item.Text))

		case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_UNSPECIFIED:
			// Ignore unspecified messages.
			continue

		default:
			// Safe fallback.
			messages = append(messages, sdk.UserMessage(item.Text))
		}
	}

	return messages
}
