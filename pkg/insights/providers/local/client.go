package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// LocalProvider implements insights.Provider using local faster-whisper + NLLB translation.
type LocalProvider struct {
	account *config.ProviderAccount
	service *config.ServiceConfig
	logger  *logrus.Entry
}

// NewProvider creates a new LocalProvider.
func NewProvider(providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
	return &LocalProvider{
		account: providerAccount,
		service: serviceConfig,
		logger:  log.WithField("service", "local"),
	}, nil
}

// CreateTranscription opens a WebSocket connection to the local whisper service.
func (p *LocalProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	whisperURL, _ := p.account.Options["whisper_url"].(string)
	if whisperURL == "" {
		return nil, fmt.Errorf("local provider: whisper_url not configured in account options")
	}

	return newTranscribeStream(ctx, whisperURL, roomId, userId, opts, p.logger)
}

// TranslateText calls the NLLB translation proxy (Azure Translation API-compatible).
func (p *LocalProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	translateURL, _ := p.account.Options["translate_url"].(string)
	if translateURL == "" {
		return nil, fmt.Errorf("local provider: translate_url not configured in account options")
	}
	if len(targetLangs) == 0 {
		return nil, fmt.Errorf("at least one target language is required")
	}

	u, err := url.Parse(translateURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse translate_url: %w", err)
	}
	q := u.Query()
	q.Add("from", sourceLang)
	for _, l := range targetLangs {
		q.Add("to", l)
	}
	u.RawQuery = q.Encode()

	requestBody, err := json.Marshal([]struct {
		Text string `json:"Text"`
	}{{Text: text}})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal translation request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create translation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("translation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("translation request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var response []struct {
		Translations []struct {
			Text string `json:"text"`
			To   string `json:"to"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode translation response: %w", err)
	}
	if len(response) == 0 || len(response[0].Translations) == 0 {
		return nil, fmt.Errorf("empty translation response from local proxy")
	}

	translations := make(map[string]string)
	for _, t := range response[0].Translations {
		translations[t.To] = t.Text
	}

	return &plugnmeet.InsightsTextTranslationResult{
		SourceText:   text,
		SourceLang:   sourceLang,
		Translations: translations,
	}, nil
}

// SynthesizeText is not supported by the local provider.
func (p *LocalProvider) SynthesizeText(_ context.Context, _ []byte) (io.ReadCloser, error) {
	return nil, fmt.Errorf("speech synthesis not supported by local provider")
}

// GetSupportedLanguages returns the list of supported languages.
func (p *LocalProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	if langs, ok := supportedLanguages[serviceType]; ok {
		result := make([]*plugnmeet.InsightsSupportedLangInfo, len(langs))
		for i := range langs {
			result[i] = &langs[i]
		}
		return result
	}
	return make([]*plugnmeet.InsightsSupportedLangInfo, 0)
}

func (p *LocalProvider) AITextChatStream(_ context.Context, _ string, _ []*plugnmeet.InsightsAITextChatContent) (<-chan *plugnmeet.InsightsAITextChatStreamResult, error) {
	return nil, nil
}

func (p *LocalProvider) AIChatTextSummarize(_ context.Context, _ string, _ []*plugnmeet.InsightsAITextChatContent) (string, uint32, uint32, error) {
	return "", 0, 0, nil
}

func (p *LocalProvider) StartBatchSummarizeAudioFile(_ context.Context, _, _, _ string) (string, string, error) {
	return "", "", nil
}

func (p *LocalProvider) CheckBatchJobStatus(_ context.Context, _ string) (*insights.BatchJobResponse, error) {
	return nil, nil
}

func (p *LocalProvider) DeleteUploadedFile(_ context.Context, _ string) error {
	return nil
}
