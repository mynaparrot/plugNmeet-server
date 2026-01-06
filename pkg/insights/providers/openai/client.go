package openai

import (
    "bufio"
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"

    "github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
    "github.com/mynaparrot/plugnmeet-server/pkg/config"
    "github.com/mynaparrot/plugnmeet-server/pkg/insights"
    "github.com/sirupsen/logrus"
)

// OpenAIProvider implements a minimal OpenAI-compatible provider.
type OpenAIProvider struct {
    account *config.ProviderAccount
    service *config.ServiceConfig
    logger  *logrus.Entry
    client  *http.Client
    baseURL string
    apiKey  string
}

// NewProvider constructs the OpenAI-compatible provider.
func NewProvider(ctx context.Context, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
    if providerAccount.Credentials.APIKey == "" {
        return nil, fmt.Errorf("openai provider requires api_key")
    }

    base := "https://api.openai.com"
    if ep, ok := providerAccount.Options["endpoint"].(string); ok && ep != "" {
        base = ep
    }

    return &OpenAIProvider{
        account: providerAccount,
        service: serviceConfig,
        logger:  log,
        client:  &http.Client{Timeout: 30 * time.Second},
        baseURL: base,
        apiKey:  providerAccount.Credentials.APIKey,
    }, nil
}

// GetBaseURL returns the configured API endpoint.
func (p *OpenAIProvider) GetBaseURL() string {
	return p.baseURL
}

// AITextChatStream implements streaming chat with SSE parsing for OpenAI-compatible endpoints.
func (p *OpenAIProvider) AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	resultChan := make(chan *plugnmeet.InsightsAITextChatStreamResult)

	go func() {
		defer close(resultChan)

		// Build messages array
		type msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		var messages []msg
		for _, h := range history {
			switch h.Role {
			case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_USER:
				messages = append(messages, msg{Role: "user", Content: h.Text})
			case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_MODEL:
				messages = append(messages, msg{Role: "assistant", Content: h.Text})
			case plugnmeet.InsightsAITextChatRole_INSIGHTS_AI_TEXT_CHAT_ROLE_SYSTEM:
				messages = append(messages, msg{Role: "system", Content: h.Text})
			default:
				messages = append(messages, msg{Role: "user", Content: h.Text})
			}
		}

		// Request streaming response from OpenAI-compatible endpoints.
		body := map[string]interface{}{
			"model":    chatModel,
			"messages": messages,
			"stream":   true,
		}

		b, _ := json.Marshal(body)
		var path string
		if strings.HasSuffix(p.baseURL, "/v1") {
			path = p.baseURL + "/chat/completions"
		} else {
			path = p.baseURL + "/v1/chat/completions"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", path, bytes.NewReader(b))
		if err != nil {
			p.logger.WithError(err).Error("openai: failed to build request")
			return
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			p.logger.WithError(err).Error("openai: request failed")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			data, _ := io.ReadAll(resp.Body)
			p.logger.Errorf("openai: non-2xx response %d: %s", resp.StatusCode, string(data))
			return
		}

		// Stream parsing: lines in SSE form like `data: {...}` and a final `data: [DONE]`.
		scanner := bufio.NewScanner(resp.Body)
		// Increase buffer size if needed
		const maxBuf = 64 * 1024
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, maxBuf)

		streamId := fmt.Sprintf("%d", time.Now().UnixMilli())
		var usagePrompt, usageCompletion, usageTotal uint32

		for scanner.Scan() {
			line := scanner.Text()
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// lines may be prefixed with "data: "
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload == "[DONE]" {
					// end of stream
					break
				}

				var ev struct {
					Choices []struct {
						Delta struct {
							Role    string `json:"role"`
							Content string `json:"content"`
						} `json:"delta"`
						FinishReason *string `json:"finish_reason"`
					} `json:"choices"`
					Usage *struct {
						PromptTokens     uint32 `json:"prompt_tokens"`
						CompletionTokens uint32 `json:"completion_tokens"`
						TotalTokens      uint32 `json:"total_tokens"`
					} `json:"usage"`
				}
				if err := json.Unmarshal([]byte(payload), &ev); err != nil {
					// ignore parse errors for partial events
					continue
				}

				// collect usage if present
				if ev.Usage != nil {
					usagePrompt = ev.Usage.PromptTokens
					usageCompletion = ev.Usage.CompletionTokens
					usageTotal = ev.Usage.TotalTokens
				}

				// emit any delta content
				for _, choice := range ev.Choices {
					if choice.Delta.Content != "" {
						resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
							Id:        streamId,
							Text:      choice.Delta.Content,
							CreatedAt: fmt.Sprintf("%d", time.Now().UnixMilli()),
						}
					}
				}
			}
		}

		// Signal end of stream with final token counts if available
		resultChan <- &plugnmeet.InsightsAITextChatStreamResult{
			Id:               streamId,
			IsLastChunk:      true,
			PromptTokens:     usagePrompt,
			CompletionTokens: usageCompletion,
			TotalTokens:      usageTotal,
			CreatedAt:        fmt.Sprintf("%d", time.Now().UnixMilli()),
		}
	}()

	return resultChan, nil
}

// AIChatTextSummarize calls the same chat completions endpoint with an explicit summarization instruction.
func (p *OpenAIProvider) AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
    // Build prompt from history and append instruction
    var builder bytes.Buffer
    for _, h := range history {
        builder.WriteString(h.Text)
        builder.WriteString("\n")
    }
    builder.WriteString("\nSummarize the above conversation in a concise paragraph.")

    body := map[string]interface{}{
        "model": summarizeModel,
        "messages": []map[string]string{
            {"role": "user", "content": builder.String()},
        },
    }

    b, _ := json.Marshal(body)
    var path string
    if strings.HasSuffix(p.baseURL, "/v1") {
        path = p.baseURL + "/chat/completions"
    } else {
        path = p.baseURL + "/v1/chat/completions"
    }
    req, err := http.NewRequestWithContext(ctx, "POST", path, bytes.NewReader(b))
    if err != nil {
        return "", 0, 0, fmt.Errorf("openai: failed to build request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+p.apiKey)
    req.Header.Set("Content-Type", "application/json")

    resp, err := p.client.Do(req)
    if err != nil {
        return "", 0, 0, fmt.Errorf("openai: request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        data, _ := io.ReadAll(resp.Body)
        return "", 0, 0, fmt.Errorf("openai: non-2xx response %d: %s", resp.StatusCode, string(data))
    }

    var parsed struct {
        Choices []struct {
            Message struct {
                Content string `json:"content"`
            } `json:"message"`
        } `json:"choices"`
        Usage struct {
            PromptTokens     uint32 `json:"prompt_tokens"`
            CompletionTokens uint32 `json:"completion_tokens"`
        } `json:"usage"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
        return "", 0, 0, fmt.Errorf("openai: failed to decode response: %w", err)
    }

    var text string
    if len(parsed.Choices) > 0 {
        text = parsed.Choices[0].Message.Content
    }

    return text, parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens, nil
}

func (p *OpenAIProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
    return nil, fmt.Errorf("CreateTranscription not implemented for openai provider")
}

func (p *OpenAIProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
    return nil, fmt.Errorf("TranslateText not implemented for openai provider")
}

func (p *OpenAIProvider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
    return nil, fmt.Errorf("SynthesizeText not implemented for openai provider")
}

func (p *OpenAIProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
    return nil
}

func (p *OpenAIProvider) StartBatchSummarizeAudioFile(ctx context.Context, filePath, summarizeModel, userPrompt string) (string, string, error) {
    return "", "", fmt.Errorf("StartBatchSummarizeAudioFile not implemented for openai provider")
}

func (p *OpenAIProvider) CheckBatchJobStatus(ctx context.Context, jobId string) (*insights.BatchJobResponse, error) {
    return nil, fmt.Errorf("CheckBatchJobStatus not implemented for openai provider")
}

func (p *OpenAIProvider) DeleteUploadedFile(ctx context.Context, fileName string) error {
    return fmt.Errorf("DeleteUploadedFile not implemented for openai provider")
}
