// Package openai implements insights.Provider against the OpenAI API and any
// OpenAI-compatible HTTP backend (LocalAI, vLLM, llama.cpp-server, whisper.cpp,
// etc.). The base_url provider option lets operators point this provider at a
// self-hosted endpoint while keeping the same Go code path as for OpenAI cloud.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

const (
	defaultTranscriptionModel = "whisper-1"
	defaultChatModel          = "gpt-4o-mini"
	defaultChunkSeconds       = 5.0
	transcriptionSampleRate   = 16000

	modeChunked  = "chunked"
	modeRealtime = "realtime"
)

// Provider implements insights.Provider for OpenAI and OpenAI-compatible APIs.
type Provider struct {
	account *config.ProviderAccount
	service *config.ServiceConfig
	client  openaisdk.Client
	logger  *logrus.Entry
}

// NewProvider builds a Provider. The api_key credential is required; the
// base_url option is optional and overrides the SDK's default endpoint.
func NewProvider(providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
	if providerAccount == nil {
		return nil, fmt.Errorf("openai: provider account is nil")
	}
	if providerAccount.Credentials.APIKey == "" {
		return nil, fmt.Errorf("openai: credentials.api_key is required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(providerAccount.Credentials.APIKey),
	}
	if baseURL, _ := providerAccount.Options["base_url"].(string); baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}

	return &Provider{
		account: providerAccount,
		service: serviceConfig,
		client:  openaisdk.NewClient(opts...),
		logger:  log.WithField("service", "openai"),
	}, nil
}

// CreateTranscription opens a transcription stream. mode: "chunked" (default)
// uploads WAV chunks to /v1/audio/transcriptions; mode: "realtime" streams
// PCM16 over WebSocket and surfaces partials.
func (p *Provider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("openai: failed to unmarshal transcription options: %w", err)
		}
	}

	model := p.serviceModel(defaultTranscriptionModel)

	switch p.transcribeMode() {
	case modeRealtime:
		return newRealtimeStream(ctx, p.account, model, roomId, userId, opts, p.logger)
	default:
		return newChunkedStream(ctx, p.client, model, p.chunkSeconds(), roomId, userId, opts, p.logger)
	}
}

// TranslateText performs translation via Chat Completions with a JSON-schema
// constrained response. One round-trip handles all target languages.
func (p *Provider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	if len(targetLangs) == 0 {
		return nil, fmt.Errorf("openai: at least one target language is required")
	}
	model := p.serviceModel(defaultChatModel)
	return translateViaChatCompletions(ctx, p.client, model, text, sourceLang, targetLangs, p.logger)
}

// SynthesizeText is intentionally not implemented: TTS via the OpenAI audio
// endpoint can be added in a follow-up; until then we surface a clear error.
func (p *Provider) SynthesizeText(_ context.Context, _ []byte) (io.ReadCloser, error) {
	return nil, fmt.Errorf("openai: speech synthesis not implemented")
}

// GetSupportedLanguages returns the static language lists for transcription
// and translation. Whisper / OpenAI translation models support far more codes
// than this list; we surface only the subset we exercise in PlugNmeet.
func (p *Provider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	if langs, ok := supportedLanguages[serviceType]; ok {
		out := make([]*plugnmeet.InsightsSupportedLangInfo, len(langs))
		for i := range langs {
			out[i] = &langs[i]
		}
		return out
	}
	return nil
}

// AITextChatStream is not supported by this provider in its current scope.
func (p *Provider) AITextChatStream(_ context.Context, _ string, _ []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return nil, nil
}

// AIChatTextSummarize is not supported by this provider in its current scope.
func (p *Provider) AIChatTextSummarize(_ context.Context, _ string, _ []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	return "", 0, 0, nil
}

// StartBatchSummarizeAudioFile is not supported by this provider.
func (p *Provider) StartBatchSummarizeAudioFile(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

// CheckBatchJobStatus is not supported by this provider.
func (p *Provider) CheckBatchJobStatus(_ context.Context, _ string) (*insights.BatchJobResponse, error) {
	return nil, nil
}

// DeleteUploadedFile is not supported by this provider.
func (p *Provider) DeleteUploadedFile(_ context.Context, _ string) error {
	return nil
}

// serviceModel reads the per-service model name from the service config,
// falling back to the supplied default. The provider account can also pin a
// model via its own options as a coarse default for both services.
func (p *Provider) serviceModel(fallback string) string {
	if p.service != nil {
		if m, _ := p.service.Options["model"].(string); m != "" {
			return m
		}
	}
	if p.account != nil {
		if m, _ := p.account.Options["model"].(string); m != "" {
			return m
		}
	}
	return fallback
}

func (p *Provider) transcribeMode() string {
	if p.account == nil {
		return modeChunked
	}
	switch m, _ := p.account.Options["mode"].(string); m {
	case modeRealtime:
		return modeRealtime
	default:
		return modeChunked
	}
}

// chunkSeconds reads chunk_seconds from the provider account options. YAML
// numbers arrive as float64 from gopkg.in/yaml.v3; ints are accepted too.
func (p *Provider) chunkSeconds() float64 {
	if p.account == nil {
		return defaultChunkSeconds
	}
	switch v := p.account.Options["chunk_seconds"].(type) {
	case float64:
		if v > 0 {
			return v
		}
	case int:
		if v > 0 {
			return float64(v)
		}
	}
	return defaultChunkSeconds
}
