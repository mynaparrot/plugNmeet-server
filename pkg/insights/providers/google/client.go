package google

import (
	"context"
	"encoding/json"
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
	client  *genai.Client
	account *config.ProviderAccount
	service *config.ServiceConfig
	logger  *logrus.Entry

	// transcribeDial and translate are replaced in tests to avoid real calls.
	transcribeDial liveTranscriptionDialer
	translate      translationRunner
}

// translationRunner translates one final transcript into the requested languages.
type translationRunner func(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error)

// runTranslation uses the injected runner when a test provided one.
func (p *GoogleProvider) runTranslation(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	if p.translate != nil {
		return p.translate(ctx, text, sourceLang, targetLangs)
	}
	return p.TranslateText(ctx, text, sourceLang, targetLangs)
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
		client:  client,
		account: providerAccount,
		service: serviceConfig,
		logger:  log,
	}, nil
}

// serviceOption reads an option without requiring a configured service block.
func (p *GoogleProvider) serviceOption(key, defaultValue string) string {
	return serviceOptionString(p.service, key, defaultValue)
}

func serviceOptionString(service *config.ServiceConfig, key, defaultValue string) string {
	if service == nil {
		return defaultValue
	}
	return service.GetOptionsString(key, defaultValue)
}

func serviceOptionInt(service *config.ServiceConfig, key string, fallback int) int {
	if service == nil {
		return fallback
	}
	return service.GetIntOption(key, fallback)
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

// TranslateText performs stateless translation with a Gemini text model.
func (p *GoogleProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	return translateText(ctx, p.client, p.service, text, sourceLang, targetLangs)
}

// SynthesizeText performs stateless text-to-speech synthesis.
func (p *GoogleProvider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
	opts := &insights.SynthesisTaskOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal synthesis options: %w", err)
		}
	}

	return synthesizeText(ctx, p.client, p.service, opts)
}

// GetSupportedLanguages implements the insights.Provider interface.
func (p *GoogleProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	langs, ok := supportedLanguages[serviceType]
	if !ok {
		return []*plugnmeet.InsightsSupportedLangInfo{}
	}

	// Return freshly allocated entries so callers cannot mutate the shared list.
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
