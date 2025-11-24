package azure

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

// AzureProvider is the main struct that implements the insights.Provider interface.
type AzureProvider struct {
	account *config.ProviderAccount
	service *config.ServiceConfig
	logger  *logrus.Entry
}

// NewProvider now accepts the full configuration structs.
func NewProvider(providerAccount *config.ProviderAccount, serviceConfig *config.ServiceConfig, log *logrus.Entry) (insights.Provider, error) {
	return &AzureProvider{
		account: providerAccount,
		service: serviceConfig,
		logger:  log,
	}, nil
}

// CreateTranscription now uses the stored credentials and parses the options.
func (p *AzureProvider) CreateTranscription(ctx context.Context, roomId, userId string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	// Read model from service config options
	model, _ := p.service.Options["model"].(string)

	// Use the stored credentials and model to create the client.
	transcribeClient, err := newTranscribeClient(&p.account.Credentials, model, p.logger)
	if err != nil {
		return nil, err
	}

	return transcribeClient.CreateTranscription(ctx, roomId, userId, opts.SpokenLang, opts.TransLangs)
}

// TranslateText implements the insights.Provider interface for stateless text translation.
func (p *AzureProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	if p.account.Credentials.APIKey == "" {
		return nil, fmt.Errorf("azure API key is not configured")
	}
	if p.account.Credentials.Region == "" {
		return nil, fmt.Errorf("azure region is not configured")
	}
	if len(targetLangs) == 0 {
		return nil, fmt.Errorf("at least one target language is required")
	}
	endpoint, ok := p.account.Options["endpoint"]
	if !ok {
		return nil, fmt.Errorf("azure endpoint is not configured")
	}

	u, err := url.Parse(endpoint.(string))
	if err != nil {
		return nil, fmt.Errorf("failed to parse azure translation endpoint: %w", err)
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
		return nil, fmt.Errorf("failed to marshal azure translation request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create azure translation http request: %w", err)
	}

	req.Header.Set("Ocp-Apim-Subscription-Key", p.account.Credentials.APIKey)
	req.Header.Set("Ocp-Apim-Subscription-Region", p.account.Credentials.Region)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute azure translation request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure translation request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var azureResponse []struct {
		Translations []struct {
			Text string `json:"text"`
			To   string `json:"to"`
		} `json:"translations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&azureResponse); err != nil {
		return nil, fmt.Errorf("failed to decode azure translation response: %w", err)
	}

	if len(azureResponse) == 0 || len(azureResponse[0].Translations) == 0 {
		return nil, fmt.Errorf("received an empty or invalid translation from azure")
	}

	// Populate the translations map
	translations := make(map[string]string)
	for _, trans := range azureResponse[0].Translations {
		translations[trans.To] = trans.Text
	}

	result := &plugnmeet.InsightsTextTranslationResult{
		SourceText:   text,
		SourceLang:   sourceLang,
		Translations: translations,
	}

	return result, nil
}

// SynthesizeText creates a ttsClient and uses it to synthesize the text.
func (p *AzureProvider) SynthesizeText(ctx context.Context, options []byte) (io.ReadCloser, error) {
	opts := &insights.SynthesisTaskOptions{}
	if err := json.Unmarshal(options, opts); err != nil {
		return nil, fmt.Errorf("failed to unmarshal synthesis options: %w", err)
	}

	// Create a new ttsClient for this specific task, using the stored credentials.
	tts, err := newTTSClient(&p.account.Credentials, p.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create tts client: %w", err)
	}

	return tts.SynthesizeText(ctx, opts.Text, opts.Language, opts.Voice)
}

// GetSupportedLanguages implements the insights.Provider interface.
// It looks up the service name in the hard-coded map from languages.go.
func (p *AzureProvider) GetSupportedLanguages(serviceType insights.ServiceType) []*plugnmeet.InsightsSupportedLangInfo {
	if langs, ok := supportedLanguages[serviceType]; ok {
		service := make([]*plugnmeet.InsightsSupportedLangInfo, len(langs))
		for i := range langs {
			service[i] = &langs[i]
		}
		return service
	}

	// Return an empty slice if the service is not found for this provider.
	return make([]*plugnmeet.InsightsSupportedLangInfo, 0)
}
