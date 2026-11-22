package google

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/genai"
)

const (
	// defaultTranslateModel is the cheapest text model that still translates well.
	defaultTranslateModel = "gemini-3.1-flash-lite"
	translateSystemPrompt = "You are a helpful translation assistant that only responds with valid JSON."
)

// translateText translates text into every requested language with one call.
func translateText(ctx context.Context, client *genai.Client, service *config.ServiceConfig, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	text = strings.TrimSpace(text)
	sourceLang = strings.TrimSpace(sourceLang)
	targets := cleanTargetLangs(targetLangs)

	result := &plugnmeet.InsightsTextTranslationResult{
		SourceText:   text,
		SourceLang:   sourceLang,
		Translations: make(map[string]string),
	}
	// Nothing to translate: skip the request, which keeps the caller cheap.
	if text == "" || len(targets) == 0 {
		return result, nil
	}

	response, err := client.Models.GenerateContent(ctx,
		resolveTranslateModel(service),
		[]*genai.Content{genai.NewContentFromText(buildTranslatePrompt(text, sourceLang, targets), genai.RoleUser)},
		&genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText(translateSystemPrompt)}},
			ResponseMIMEType:  "application/json",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to execute translation request: %w", err)
	}

	raw := responseText(response)
	if raw == "" {
		return nil, fmt.Errorf("empty translation content in response")
	}

	translations, err := parseTranslations(raw, targets)
	if err != nil {
		return nil, err
	}
	result.Translations = translations

	return result, nil
}

// resolveTranslateModel picks the text model. The transcription and TTS models
// cannot answer a GenerateContent call, so a service that names one falls back
// to the default translation model.
func resolveTranslateModel(service *config.ServiceConfig) string {
	if model := strings.TrimSpace(serviceOptionString(service, "translate_model", "")); model != "" {
		return model
	}
	if model := strings.TrimSpace(serviceOptionString(service, "model", "")); model != "" && !isSpeechOnlyModel(model) {
		return model
	}
	return defaultTranslateModel
}

// isSpeechOnlyModel reports whether a model name belongs to a speech model.
func isSpeechOnlyModel(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "transcribe") || strings.Contains(model, "tts") || strings.Contains(model, "live")
}

// cleanTargetLangs trims and deduplicates the requested languages.
func cleanTargetLangs(targetLangs []string) []string {
	targets := make([]string, 0, len(targetLangs))
	seen := make(map[string]bool, len(targetLangs))

	for _, lang := range targetLangs {
		lang = strings.TrimSpace(lang)
		key := strings.ToLower(lang)
		if lang == "" || seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, lang)
	}

	return targets
}

func buildTranslatePrompt(text, sourceLang string, targets []string) string {
	source := sourceLang
	if source == "" {
		source = "the detected language"
	}

	return fmt.Sprintf(
		"Translate from %s to: %s.\n"+
			"Return only a JSON object where each key is one requested language code and each value is the translated text.\n"+
			"Example: {\"es\":\"translated text\"}\n"+
			"Do not use keys like language_code or translated_text.\n"+
			"Text:\n%s",
		source,
		strings.Join(targets, ", "),
		text,
	)
}

// responseText concatenates the text parts of the first candidate.
func responseText(response *genai.GenerateContentResponse) string {
	if response == nil || len(response.Candidates) == 0 {
		return ""
	}
	candidate := response.Candidates[0]
	if candidate == nil || candidate.Content == nil {
		return ""
	}

	var builder strings.Builder
	for _, part := range candidate.Content.Parts {
		if part != nil {
			builder.WriteString(part.Text)
		}
	}

	return strings.TrimSpace(builder.String())
}

// parseTranslations keeps the requested languages out of the model's JSON reply,
// matching keys case-insensitively.
func parseTranslations(raw string, targets []string) (map[string]string, error) {
	content := stripCodeFence(raw)

	var payload map[string]string
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal translated JSON content: %w. Raw content: %s", err, content)
	}

	lowered := make(map[string]string, len(payload))
	for key, value := range payload {
		if value = strings.TrimSpace(value); value != "" {
			lowered[strings.ToLower(strings.TrimSpace(key))] = value
		}
	}

	translations := make(map[string]string, len(targets))
	for _, target := range targets {
		if value, ok := lowered[strings.ToLower(target)]; ok {
			translations[target] = value
		}
	}

	// Fallback for a single language answered in the wrong shape, e.g.
	// {"language_code":"es","translated_text":"hola"}.
	if len(translations) == 0 && len(targets) == 1 {
		target := targets[0]
		if value, ok := lowered["translated_text"]; ok {
			translations[target] = value
		} else if value, ok := lowered["language_code"]; ok && !strings.EqualFold(value, target) {
			translations[target] = value
		}
	}

	if len(translations) == 0 {
		return nil, fmt.Errorf("no requested translations found in response. Raw content: %s", content)
	}

	return translations, nil
}

// stripCodeFence removes the markdown fence a model sometimes wraps JSON in.
func stripCodeFence(raw string) string {
	content := strings.TrimSpace(raw)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	return strings.TrimSpace(content)
}
