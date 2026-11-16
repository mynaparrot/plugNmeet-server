package openai

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// chatStream handles the real-time streaming chat with OpenAI SDK.
func chatStream(ctx context.Context, client sdk.Client, service *config.ServiceConfig, model string, history []*plugnmeet.InsightsAITextChatContent, logger *logrus.Entry) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult, 50)
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

			select {
			case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
				Id:        streamId,
				Text:      content,
				CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
			}:
			case <-ctx.Done():
				return
			}
		}

		if err := stream.Err(); err != nil {
			logger.WithError(err).Error("failed to execute openai chat stream request")
		}

		// Signal the end of the stream.
		select {
		case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
			Id:          streamId,
			IsLastChunk: true,
			CreatedAt:   fmt.Sprintf("%d", time.Now().UnixMilli()),
		}:
		case <-ctx.Done():
			return
		}
	}()

	return resultChan, nil
}

// maxToolRounds caps the number of tool-execution rounds one text chat request
// may use; the loop runs one extra round afterwards for the final summary. A
// model that keeps asking for tools is stopped after this many rounds so a
// single request can never loop forever.
const maxToolRounds = 8

// streamedToolCall is one tool call reassembled from the streamed deltas.
type streamedToolCall struct {
	id        string
	name      string
	arguments string
}

// chatStreamWithTools streams a chat turn, executing the tools the model asks
// for and feeding their results back until the model answers in plain text.
func chatStreamWithTools(ctx context.Context, client sdk.Client, service *config.ServiceConfig, model string, history []*plugnmeet.InsightsAITextChatContent, tools *insights.AITextChatToolRunner, logger *logrus.Entry) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult, 50)
	streamId := uuid.NewString()

	if model == "" {
		model = service.GetOptionsString("chat_model", sdk.ChatModelGPT5_4)
	}

	params := sdk.ChatCompletionNewParams{
		Model:         sdk.ChatModel(model),
		Messages:      toOpenAIMessageParams(history),
		StreamOptions: sdk.ChatCompletionStreamOptionsParam{IncludeUsage: sdk.Bool(true)},
	}
	canUseTools := tools != nil && len(tools.Tools) > 0 && tools.Execute != nil
	if canUseTools {
		params.Tools = buildChatTools(tools.Tools)
	}

	go func() {
		defer close(resultChan)

		var promptTokens, completionTokens, totalTokens uint32

		for round := 0; round <= maxToolRounds; round++ {
			stream := client.Chat.Completions.NewStreaming(ctx, params)

			var text strings.Builder
			calls := make([]*streamedToolCall, 0, 2)
			byIndex := make(map[int64]*streamedToolCall, 2)

			for stream.Next() {
				evt := stream.Current()

				// The usage chunk arrives with empty choices, so it is read first.
				if evt.Usage.TotalTokens != 0 {
					promptTokens += uint32(evt.Usage.PromptTokens)
					completionTokens += uint32(evt.Usage.CompletionTokens)
					totalTokens += uint32(evt.Usage.TotalTokens)
				}
				if len(evt.Choices) == 0 {
					continue
				}

				delta := evt.Choices[0].Delta
				if delta.Content != "" {
					text.WriteString(delta.Content)
					select {
					case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
						Id:        streamId,
						Text:      delta.Content,
						CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
					}:
					case <-ctx.Done():
						return
					}
				}

				for _, tc := range delta.ToolCalls {
					call, ok := byIndex[tc.Index]
					if !ok {
						call = new(streamedToolCall)
						byIndex[tc.Index] = call
						calls = append(calls, call)
					}
					// ID and name arrive whole with the first delta, the arguments
					// are streamed in fragments.
					if tc.ID != "" {
						call.id = tc.ID
					}
					if tc.Function.Name != "" {
						call.name = tc.Function.Name
					}
					call.arguments += tc.Function.Arguments
				}
			}

			if err := stream.Err(); err != nil {
				logger.WithError(err).Error("failed to execute openai chat stream request")
				break
			}
			if !canUseTools || len(calls) == 0 || round == maxToolRounds {
				break
			}

			params.Messages = append(params.Messages, assistantToolCallMessage(text.String(), calls))
			for _, call := range calls {
				callID := call.id
				if callID == "" {
					callID = uuid.NewString()
				}
				out, isErr := tools.Execute(ctx, callID, call.name, call.arguments)
				params.Messages = append(params.Messages, sdk.ToolMessage(formatToolOutputJSON(out, isErr), callID))
			}
		}

		// Signal the end of the stream.
		select {
		case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
			Id:               streamId,
			IsLastChunk:      true,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			CreatedAt:        fmt.Sprintf("%d", time.Now().UnixMilli()),
		}:
		case <-ctx.Done():
			return
		}
	}()

	return resultChan, nil
}

// buildChatTools converts the MCP shaped declarations into the SDK's tool params.
func buildChatTools(specs []*insights.MCPToolSpec) []sdk.ChatCompletionToolUnionParam {
	params := make([]sdk.ChatCompletionToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		if spec == nil || spec.Name == "" {
			continue
		}
		params = append(params, sdk.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        spec.Name,
			Description: sdk.String(spec.Description),
			Parameters:  shared.FunctionParameters(spec.InputSchema),
		}))
	}
	return params
}

// assistantToolCallMessage replays the model's tool request into the messages
// so the following tool replies have something to answer to.
func assistantToolCallMessage(content string, calls []*streamedToolCall) sdk.ChatCompletionMessageParamUnion {
	assistant := sdk.ChatCompletionAssistantMessageParam{}
	if content != "" {
		assistant.Content.OfString = param.NewOpt(content)
	}
	assistant.ToolCalls = make([]sdk.ChatCompletionMessageToolCallUnionParam, 0, len(calls))
	for _, call := range calls {
		assistant.ToolCalls = append(assistant.ToolCalls, sdk.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
				ID: call.id,
				Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      call.name,
					Arguments: call.arguments,
				},
			},
		})
	}
	return sdk.ChatCompletionMessageParamUnion{OfAssistant: &assistant}
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

	return resp.Choices[0].Message.Content, uint32(resp.Usage.PromptTokens), uint32(resp.Usage.CompletionTokens), nil
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
