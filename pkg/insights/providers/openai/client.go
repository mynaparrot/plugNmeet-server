package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	openAiSdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
)

// OpenAIProvider implements the insights.Provider interface for OpenAI's services.
type OpenAIProvider struct {
	account      *config.ProviderAccount
	service      *config.ServiceConfig
	openAiClient openAiSdk.Client
	logger       *logrus.Entry
	redis        redis.UniversalClient
}

var _ insights.ToolTextChatProvider = (*OpenAIProvider)(nil)

// NewProvider creates a new OpenAI provider.
func NewProvider(ctx context.Context, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry, redis redis.UniversalClient) (insights.Provider, error) {
	if providerAccount.Credentials.APIKey == "" {
		return nil, fmt.Errorf("openai provider requires api_key")
	}
	baseURL := providerAccount.GetOptionsString("endpoint", defaultBaseURL)

	client := openAiSdk.NewClient(
		option.WithAPIKey(providerAccount.Credentials.APIKey),
		option.WithBaseURL(strings.TrimSuffix(baseURL, "/")),
	)

	return &OpenAIProvider{
		account:      providerAccount,
		service:      serviceConfig,
		openAiClient: client,
		logger:       log,
		redis:        redis,
	}, nil
}

// CreateRealtimeSession opens a bidirectional agent session. Live models use the
// Live SDK (Responses delegation); everything else uses the Realtime SDK, which
// also serves OpenAI-compatible self-hosted servers.
func (p *OpenAIProvider) CreateRealtimeSession(ctx context.Context, model string, opts *insights.RealtimeSessionOptions) (insights.RealtimeSession, error) {
	if opts == nil {
		opts = &insights.RealtimeSessionOptions{}
	}

	model = p.resolveRealtimeModel(model)
	if isLiveModel(model) {
		return p.createLiveSession(ctx, model, opts)
	}

	return p.createRealtimeAgentSession(ctx, model, opts)
}

func (p *OpenAIProvider) resolveRealtimeModel(model string) string {
	if model = strings.TrimSpace(model); model != "" {
		return model
	}

	if resolved := p.serviceOption("realtime_model", p.serviceOption("model", "")); resolved != "" {
		return resolved
	}

	return defaultRealtimeAgentModel
}

func isLiveModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-live")
}

// serviceOption reads an option without requiring a configured service block.
func (p *OpenAIProvider) serviceOption(key, defaultValue string) string {
	if p.service == nil {
		return defaultValue
	}
	return p.service.GetOptionsString(key, defaultValue)
}

// realtimeOption prefers an explicit session override over the service config.
func (p *OpenAIProvider) realtimeOption(opts *insights.RealtimeSessionOptions, key, defaultValue string) string {
	if value, ok := opts.Extra[key].(string); ok {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return p.serviceOption(key, defaultValue)
}

// realtimeRequestOptions builds the auth headers for Realtime and Live sockets.
// Self-hosted servers commonly expect the key in a custom header instead of a
// bearer token.
func (p *OpenAIProvider) realtimeRequestOptions() []option.RequestOption {
	apiKey := p.account.Credentials.APIKey
	authHeader := strings.TrimSpace(p.serviceOption("realtime_auth_header", "Authorization"))
	if authHeader == "" || strings.EqualFold(authHeader, "Authorization") {
		return []option.RequestOption{option.WithHeader("Authorization", "Bearer "+apiKey)}
	}

	return []option.RequestOption{
		option.WithHeaderDel("Authorization"),
		option.WithHeader(authHeader, apiKey),
	}
}

// CreateTranscription initializes a real-time transcription stream using OpenAI's realtime API.
func (p *OpenAIProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	// Create a new realtime client for this specific task.
	rc, err := newRealtimeClient(p.account, p.service, p.logger, p)
	if err != nil {
		return nil, fmt.Errorf("failed to create realtime client: %w", err)
	}

	return rc.CreateTranscription(ctx, roomId, userId, opts)
}

// TranslateText performs stateless translation using the chat completion API.
func (p *OpenAIProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	return translateText(ctx, p.openAiClient, p.service, text, sourceLang, targetLangs)
}

// SynthesizeText performs stateless text-to-speech synthesis.
func (p *OpenAIProvider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
	opts := &insights.SynthesisTaskOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal synthesis options: %w", err)
		}
	}

	return synthesizeText(ctx, p.openAiClient, p.service, opts.Text, opts.Language, opts.Voice)
}

// GetSupportedLanguages implements the insights.Provider interface.
func (p *OpenAIProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	if langs, ok := supportedLanguages[serviceType]; ok {
		// Return a new slice with freshly allocated messages so callers cannot
		// mutate the shared map entries and we avoid copying protobuf internal
		// state.
		serviceLangs := make([]*plugnmeet.InsightsSupportedLangInfo, len(langs))
		for i := range langs {
			serviceLangs[i] = &plugnmeet.InsightsSupportedLangInfo{
				Code:   langs[i].Code,
				Name:   langs[i].Name,
				Locale: langs[i].Locale,
			}
		}
		return serviceLangs
	}
	return []*plugnmeet.InsightsSupportedLangInfo{}
}

// AITextChatStream sends a prompt with history and streams back the AI's response.
func (p *OpenAIProvider) AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return chatStream(ctx, p.openAiClient, p.service, chatModel, history, p.logger)
}

// AITextChatStreamWithTools streams a chat turn that may call tools.
func (p *OpenAIProvider) AITextChatStreamWithTools(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent, tools *insights.AITextChatToolRunner) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return chatStreamWithTools(ctx, p.openAiClient, p.service, chatModel, history, tools, p.logger)
}

// AIChatTextSummarize summarizes a conversation history.
func (p *OpenAIProvider) AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (summaryText string, promptTokens uint32, completionTokens uint32, err error) {
	return summarize(ctx, p.openAiClient, p.service, summarizeModel, history)
}

// DeleteUploadedFile is a no-op for OpenAI as we process local files.
func (p *OpenAIProvider) DeleteUploadedFile(ctx context.Context, fileName string) error {
	return nil
}
