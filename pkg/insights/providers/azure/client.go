package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
)

// AzureProvider is the main struct that implements the insights.Provider interface.
type AzureProvider struct {
	creds  *config.CredentialsConfig // Store the specific credentials for this provider instance
	model  string                    // Store the model from the service config
	logger *logrus.Entry
}

// NewProvider now accepts the specific credentials for this provider instance.
func NewProvider(creds *config.CredentialsConfig, model string, log *logrus.Entry) (*AzureProvider, error) {
	return &AzureProvider{
		creds:  creds,
		model:  model,
		logger: log,
	}, nil
}

// CreateTranscription now uses the stored credentials and parses the options.
func (p *AzureProvider) CreateTranscription(ctx context.Context, roomID, userID string, options []byte) (insights.TranscriptionStream, error) {
	opts := &insights.TranscriptionOptions{
		SpokenLang: "en-US",
	}
	if len(options) > 0 {
		if err := json.Unmarshal(options, opts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal transcription options: %w", err)
		}
	}

	// Use the stored credentials and model to create the client.
	transcribeClient, err := newTranscribeClient(p.creds, p.model, p.logger)
	if err != nil {
		return nil, err
	}

	return transcribeClient.CreateTranscription(ctx, roomID, userID, opts.SpokenLang, opts.TransLangs)
}

// TranslateText implements the insights.Provider interface for stateless text translation.
func (p *AzureProvider) TranslateText(ctx context.Context, text, sourceLang string, targetLangs []string) (<-chan *insights.TextTranslationResult, error) {
	if p.creds.APIKey == "" {
		return nil, fmt.Errorf("azure API key is not configured")
	}
	if p.creds.Region == "" {
		return nil, fmt.Errorf("azure region is not configured")
	}
	if len(targetLangs) == 0 {
		return nil, fmt.Errorf("at least one target language is required")
	}

	results := make(chan *insights.TextTranslationResult, 1)

	go func() {
		defer close(results)

		// Construct the 'to' query parameter for multiple languages
		endpoint := fmt.Sprintf("https://api.cognitive.microsofttranslator.com/translate?api-version=3.0&from=%s&to=%s", sourceLang, strings.Join(targetLangs, "&to="))
		requestBody, err := json.Marshal([]struct {
			Text string `json:"Text"`
		}{{Text: text}})
		if err != nil {
			p.logger.WithError(err).Error("failed to marshal azure translation request body")
			return
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(requestBody))
		if err != nil {
			p.logger.WithError(err).Error("failed to create azure translation http request")
			return
		}

		req.Header.Set("Ocp-Apim-Subscription-Key", p.creds.APIKey)
		req.Header.Set("Ocp-Apim-Subscription-Region", p.creds.Region)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			p.logger.WithError(err).Error("failed to execute azure translation request")
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			p.logger.Errorf("azure translation request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
			return
		}

		var azureResponse []struct {
			Translations []struct {
				Text string `json:"text"`
				To   string `json:"to"`
			} `json:"translations"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&azureResponse); err != nil {
			p.logger.WithError(err).Error("failed to decode azure translation response")
			return
		}

		if len(azureResponse) == 0 || len(azureResponse[0].Translations) == 0 {
			p.logger.Error("received an empty or invalid translation from azure")
			return
		}

		// Populate the translations map
		translations := make(map[string]string)
		for _, trans := range azureResponse[0].Translations {
			translations[trans.To] = trans.Text
		}

		result := &insights.TextTranslationResult{
			Text:         text,
			SourceLang:   sourceLang,
			Translations: translations,
		}

		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
	}()

	return results, nil
}

// GetSupportedLanguages implements the insights.Provider interface.
// It looks up the service name in the hard-coded map from languages.go.
func (p *AzureProvider) GetSupportedLanguages(serviceName string) []config.LanguageInfo {
	if langs, ok := supportedLanguages[serviceName]; ok {
		return langs
	}
	// Return an empty slice if the service is not found for this provider.
	return []config.LanguageInfo{}
}
