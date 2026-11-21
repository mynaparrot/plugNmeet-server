package google

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

// GoogleProvider implements the insights.Provider interface for Google's AI services.
type GoogleProvider struct {
	client *genai.Client
	logger *logrus.Entry
}

var _ insights.ToolTextChatProvider = (*GoogleProvider)(nil)

// NewProvider creates a new Google AI provider.
func NewProvider(ctx context.Context, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
	if providerAccount.Credentials.APIKey == "" {
		return nil, fmt.Errorf("google provider requires api_key")
	}

	clientConfig := &genai.ClientConfig{
		APIKey: providerAccount.Credentials.APIKey,
	}
	if httpOptions := resolveHTTPOptions(providerAccount, serviceConfig); httpOptions != nil {
		clientConfig.HTTPOptions = *httpOptions
	}

	client, err := genai.NewClient(ctx, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &GoogleProvider{
		client: client,
		logger: log,
	}, nil
}

// resolveHTTPOptions builds the HTTP options for the genai client from the
// provider account options and the service options. The account is shared by
// every service using it, so a service level api_version overrides the account
// one. It returns nil when nothing is configured so the genai SDK keeps its own
// defaults. Note that the Gemini Live WebSocket API (BidiGenerateContent)
// currently requires "v1beta", so Live users must set api_version on the
// ai_agent service options explicitly.
func resolveHTTPOptions(providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig) *genai.HTTPOptions {
	apiVersion := strings.TrimSpace(providerAccount.GetOptionsString("api_version", ""))
	if serviceConfig != nil {
		if serviceAPIVersion := strings.TrimSpace(serviceConfig.GetOptionsString("api_version", "")); serviceAPIVersion != "" {
			apiVersion = serviceAPIVersion
		}
	}

	endpoint := strings.TrimSpace(providerAccount.GetOptionsString("endpoint", ""))

	serviceAuthHeader := ""
	if serviceConfig != nil {
		serviceAuthHeader = serviceConfig.GetOptionsString("realtime_auth_header", "")
	}
	authHeader := strings.TrimSpace(providerAccount.GetOptionsString("auth_header", serviceAuthHeader))

	if endpoint == "" && authHeader == "" && apiVersion == "" {
		return nil
	}

	httpOptions := &genai.HTTPOptions{}
	if apiVersion != "" {
		httpOptions.APIVersion = apiVersion
	}
	if endpoint != "" {
		httpOptions.BaseURL = endpoint
	}

	if authHeader != "" {
		httpOptions.Headers = http.Header{}
		if strings.EqualFold(authHeader, "Authorization") {
			httpOptions.Headers.Set("Authorization", "Bearer "+providerAccount.Credentials.APIKey)
		} else {
			httpOptions.Headers.Set(authHeader, providerAccount.Credentials.APIKey)
		}
	}

	return httpOptions
}

// AITextChatStream delegates to the chat implementation.
func (p *GoogleProvider) AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return newChatStream(ctx, p.client, chatModel, history, p.logger)
}

// AITextChatStreamWithTools streams a chat turn that may call functions.
func (p *GoogleProvider) AITextChatStreamWithTools(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent, tools *insights.AITextChatToolRunner) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return newChatStreamWithTools(ctx, p.client, chatModel, history, tools, p.logger)
}

// AIChatTextSummarize delegates to the chat implementation.
func (p *GoogleProvider) AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	return summarize(ctx, p.client, summarizeModel, history)
}

func (p *GoogleProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	return nil, fmt.Errorf("CreateTranscription is not implemented for the google provider")
}

func (p *GoogleProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	return nil, fmt.Errorf("TranslateText is not implemented for the google provider")
}

func (p *GoogleProvider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
	return nil, fmt.Errorf("SynthesizeText is not implemented for the google provider")
}

func (p *GoogleProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	return nil // Not applicable
}
