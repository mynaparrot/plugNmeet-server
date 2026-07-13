package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
	"github.com/openai/openai-go/v3/shared/constant"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// translateText performs stateless translation using the OpenAI SDK.
func translateText(ctx context.Context, client sdk.Client, service *config.ServiceConfig, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
	text = strings.TrimSpace(text)
	sourceLang = strings.TrimSpace(sourceLang)

	if text == "" || len(targetLangs) == 0 {
		return nil, fmt.Errorf("text and at least one target language are required")
	}

	model := service.GetOptionsString("model", sdk.ChatModelGPT5_4Mini)

	targets := make([]string, 0, len(targetLangs))
	seen := make(map[string]bool)

	for _, lang := range targetLangs {
		lang = strings.TrimSpace(lang)
		if lang == "" || seen[lang] {
			continue
		}

		seen[lang] = true
		targets = append(targets, lang)
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("at least one valid target language is required")
	}

	prompt := fmt.Sprintf(
		"Translate from %s to: %s.\n"+
			"Return only a JSON object where each key is one requested language code and each value is the translated text.\n"+
			"Example: {\"es\":\"translated text\"}\n"+
			"Do not use keys like language_code or translated_text.\n"+
			"Text:\n%s",
		sourceLang,
		strings.Join(targets, ","),
		text,
	)

	params := sdk.ChatCompletionNewParams{
		Model: model,
		Messages: []sdk.ChatCompletionMessageParamUnion{
			sdk.SystemMessage("You are a helpful translation assistant that only responds with valid JSON."),
			sdk.UserMessage(prompt),
		},
		ResponseFormat: sdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{
				Type: constant.JSONObject("json_object"),
			},
		},
	}

	completion, err := client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to execute translation request: %w", err)
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no translation content found in response")
	}

	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	if content == "" {
		return nil, fmt.Errorf("empty translation content in response")
	}

	var translations map[string]string
	if err := json.Unmarshal([]byte(content), &translations); err != nil {
		return nil, fmt.Errorf(
			"failed to unmarshal translated JSON content: %w. Raw content: %s",
			err,
			content,
		)
	}

	filtered := make(map[string]string, len(targets))
	for _, lang := range targets {
		if value, ok := translations[lang]; ok && strings.TrimSpace(value) != "" {
			filtered[lang] = strings.TrimSpace(value)
		}
	}

	// Simple fallback for responses like:
	// {"language_code":"es","translated_text":"hola"}
	if len(filtered) == 0 && len(targets) == 1 {
		target := targets[0]

		if value, ok := translations["translated_text"]; ok && strings.TrimSpace(value) != "" {
			filtered[target] = strings.TrimSpace(value)
		}

		// Simple fallback for malformed response like:
		// {"language_code":"No sé por qué está pasando esto."}
		if len(filtered) == 0 {
			if value, ok := translations["language_code"]; ok && strings.TrimSpace(value) != "" {
				value = strings.TrimSpace(value)

				// Avoid storing "es" as the translation if language_code actually contains the code.
				if strings.ToLower(value) != strings.ToLower(target) {
					filtered[target] = value
				}
			}
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no requested translations found in response. Raw content: %s", content)
	}

	return &plugnmeet.InsightsTextTranslationResult{
		SourceText:   text,
		SourceLang:   sourceLang,
		Translations: filtered,
	}, nil
}
