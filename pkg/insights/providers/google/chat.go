package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

// newChatStream handles the real-time streaming chat with Gemini using a stateful chat session.
func newChatStream(ctx context.Context, client *genai.Client, model string, history []*plugnmeet.InsightsAITextChatContent, logger *logrus.Entry) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult)
	streamId := uuid.NewString()

	go func() {
		defer close(resultChan)

		// The last message is the new prompt. Separate it from the initial history.
		lastMessage := history[len(history)-1]
		initialHistory := toGenaiContent(history[:len(history)-1])

		// Create a new stateful chat session with the history BEFORE the new prompt.
		chat, err := client.Chats.Create(ctx, model, nil, initialHistory)
		if err != nil {
			logger.WithError(err).Error("failed to create gemini chat session")
			return
		}

		prompt := genai.Part{Text: lastMessage.Text}
		var streamUsageMetadata *genai.GenerateContentResponseUsageMetadata

		// Send the new prompt and stream the response
		for chunk, err := range chat.SendMessageStream(ctx, prompt) {
			if err != nil {
				logger.WithError(err).Error("error getting next chunk from gemini stream")
				return
			}
			if chunk != nil {
				var textContent strings.Builder
				for _, cand := range chunk.Candidates {
					if cand.Content != nil {
						for _, part := range cand.Content.Parts {
							textContent.WriteString(part.Text)
						}
					}
				}
				// The UsageMetadata is expected to be populated in the stream.
				// We capture the last non-nil one, assuming it's the final, cumulative count.
				if chunk.UsageMetadata != nil {
					streamUsageMetadata = chunk.UsageMetadata
				}

				select {
				case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
					Id:        streamId,
					Text:      textContent.String(),
					CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
				}:
				case <-ctx.Done():
					return
				}
			}
		}

		var promptTokens, completionTokens, totalTokens uint32
		if streamUsageMetadata != nil {
			promptTokens = uint32(streamUsageMetadata.PromptTokenCount)
			completionTokens = uint32(streamUsageMetadata.CandidatesTokenCount)
			totalTokens = uint32(streamUsageMetadata.TotalTokenCount)
		}

		// Signal the end of the stream with final token counts
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

// maxToolRounds caps the number of tool-execution rounds one text chat request
// may use; the loop runs one extra round afterwards for the final summary, so a
// model that keeps asking for tools cannot loop forever.
const maxToolRounds = 8

// newChatStreamWithTools streams a chat turn, running the functions the model
// asks for and feeding their results back until it answers in plain text.
func newChatStreamWithTools(ctx context.Context, client *genai.Client, model string, history []*plugnmeet.InsightsAITextChatContent, tools *insights.AITextChatToolRunner, logger *logrus.Entry) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult)
	streamId := uuid.NewString()

	go func() {
		defer close(resultChan)

		lastMessage := history[len(history)-1]
		initialHistory := toGenaiContent(history[:len(history)-1])

		canUseTools := tools != nil && len(tools.Tools) > 0 && tools.Execute != nil
		var config *genai.GenerateContentConfig
		if canUseTools {
			config = &genai.GenerateContentConfig{
				Tools: []*genai.Tool{{FunctionDeclarations: buildFunctionDeclarations(tools.Tools)}},
			}
		}

		chat, err := client.Chats.Create(ctx, model, config, initialHistory)
		if err != nil {
			logger.WithError(err).Error("failed to create gemini chat session")
			return
		}

		prompt := []genai.Part{{Text: lastMessage.Text}}
		var promptTokens, completionTokens, totalTokens uint32
		var failed bool

		for round := 0; round <= maxToolRounds; round++ {
			var functionCalls []*genai.FunctionCall

			for chunk, err := range chat.SendMessageStream(ctx, prompt...) {
				if err != nil {
					logger.WithError(err).Error("error getting next chunk from gemini stream")
					failed = true
					break
				}
				if chunk == nil {
					continue
				}

				functionCalls = append(functionCalls, chunk.FunctionCalls()...)
				if chunk.UsageMetadata != nil {
					promptTokens += uint32(chunk.UsageMetadata.PromptTokenCount)
					completionTokens += uint32(chunk.UsageMetadata.CandidatesTokenCount)
					totalTokens += uint32(chunk.UsageMetadata.TotalTokenCount)
				}

				var textContent strings.Builder
				for _, cand := range chunk.Candidates {
					if cand.Content == nil {
						continue
					}
					for _, part := range cand.Content.Parts {
						textContent.WriteString(part.Text)
					}
				}
				if textContent.Len() == 0 {
					continue
				}

				select {
				case resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
					Id:        streamId,
					Text:      textContent.String(),
					CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
				}:
				case <-ctx.Done():
					return
				}
			}

			if failed || !canUseTools || len(functionCalls) == 0 || round == maxToolRounds {
				break
			}

			// Answer every requested function in one turn, then let the model
			// continue with the results.
			prompt = make([]genai.Part, 0, len(functionCalls))
			for _, fc := range functionCalls {
				out, isErr := tools.Execute(ctx, fc.ID, fc.Name, functionArgsJSON(fc.Args))
				prompt = append(prompt, genai.Part{
					FunctionResponse: &genai.FunctionResponse{
						ID:       fc.ID,
						Name:     fc.Name,
						Response: functionResponsePayload(fc.Name, out, isErr),
					},
				})
			}
		}

		// Signal the end of the stream with the final token counts.
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

// functionArgsJSON renders the arguments the model supplied for one function call.
func functionArgsJSON(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// summarize uses the non-streaming API to get a summary of a conversation.
func summarize(ctx context.Context, client *genai.Client, model string, history []*plugnmeet.InsightsAITextChatContent) (summaryText string, promptTokens uint32, completionTokens uint32, err error) {
	genaiHistory := toGenaiContent(history)

	// Add a specific instruction for summarization at the end of the content slice.
	genaiHistory = append(genaiHistory, genai.NewContentFromText("Summarize the following conversation in a concise paragraph.", genai.RoleUser))

	// Call GenerateContent with the model name and the complete history slice
	var resp *genai.GenerateContentResponse
	resp, err = client.Models.GenerateContent(ctx, model, genaiHistory, nil)
	if err != nil {
		err = fmt.Errorf("failed to generate summary: %w", err)
		return
	}

	var textBuilder strings.Builder
	for _, cand := range resp.Candidates {
		if cand.Content != nil {
			for _, part := range cand.Content.Parts {
				textBuilder.WriteString(part.Text)
			}
		}
	}

	if textBuilder.Len() == 0 {
		err = fmt.Errorf("no summary content found in response")
		return
	}

	summaryText = textBuilder.String()
	if resp.UsageMetadata != nil {
		promptTokens = uint32(resp.UsageMetadata.PromptTokenCount)
		completionTokens = uint32(resp.UsageMetadata.CandidatesTokenCount)
	}

	return
}
