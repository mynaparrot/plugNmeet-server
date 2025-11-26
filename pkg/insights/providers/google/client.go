package google

import (
	"context"
	"fmt"
	"io"

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

// NewProvider creates a new Google AI provider.
func NewProvider(ctx context.Context, providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
	if providerAccount.Credentials.APIKey == "" {
		return nil, fmt.Errorf("google provider requires api_key")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: providerAccount.Credentials.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &GoogleProvider{
		client: client,
		logger: log,
	}, nil
}

// AITextChatStream delegates to the chat implementation.
func (p *GoogleProvider) AITextChatStream(ctx context.Context, chatModel string, history []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return newChatStream(ctx, p.client, chatModel, history, p.logger)
}

// AIChatTextSummarize delegates to the chat implementation.
func (p *GoogleProvider) AIChatTextSummarize(ctx context.Context, summarizeModel string, history []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	return summarize(ctx, p.client, summarizeModel, history)
}

// The following methods are not implemented by the Google provider for this service.
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
