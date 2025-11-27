package google

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

				resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
					Id:        streamId,
					Text:      textContent.String(),
					CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
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
		resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
			Id:               streamId,
			IsLastChunk:      true,
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			CreatedAt:        fmt.Sprintf("%d", time.Now().UnixMilli()),
		}
	}()

	return resultChan, nil
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
