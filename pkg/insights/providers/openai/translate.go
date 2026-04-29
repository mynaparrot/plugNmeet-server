package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	openaisdk "github.com/openai/openai-go/v3"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

// translateViaChatCompletions uses the Chat Completions endpoint with a
// dynamically-shaped JSON Schema to translate a single source string into all
// requested target languages in one round-trip. The schema's properties are
// the target language codes, so a Strict-mode response is forced to populate
// exactly the keys we expect.
//
// We deliberately use Chat Completions rather than the newer Responses API:
// every OpenAI-compatible self-hosted backend (llama.cpp-server, LocalAI,
// vLLM, Ollama) implements /v1/chat/completions, while the Responses API is
// OpenAI-specific. Keeping translation portable matters more here than using
// the latest API surface.
func translateViaChatCompletions(parentCtx context.Context, client openaisdk.Client, model, text, sourceLang string, targetLangs []string, log *logrus.Entry) (*plugnmeet.InsightsTextTranslationResult, error) {
	if strings.TrimSpace(text) == "" {
		// Skip the round-trip; an empty source maps to empty translations
		// and the model would either refuse or hallucinate filler.
		empty := make(map[string]string, len(targetLangs))
		for _, l := range targetLangs {
			empty[l] = ""
		}
		return &plugnmeet.InsightsTextTranslationResult{
			SourceText:   text,
			SourceLang:   sourceLang,
			Translations: empty,
		}, nil
	}

	schema := buildTranslationSchema(targetLangs)
	systemPrompt := "You are a professional translation engine. Translate the user's text faithfully and idiomatically. Preserve meaning, tone, named entities, and numbers. Do not add explanations. Respond ONLY with the JSON object that matches the response schema."

	srcDescriptor := sourceLang
	if srcDescriptor == "" {
		srcDescriptor = "auto-detect"
	}
	userPrompt := fmt.Sprintf(
		"Source language: %s\nTarget languages (ISO 639-1): %s\n\nText to translate:\n%s",
		srcDescriptor,
		strings.Join(targetLangs, ", "),
		text,
	)

	schemaParam := openaisdk.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:        "translations",
		Description: openaisdk.String("Map of ISO 639-1 target language code to translated text."),
		Schema:      schema,
		Strict:      openaisdk.Bool(true),
	}

	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
	defer cancel()

	resp, err := client.Chat.Completions.New(ctx, openaisdk.ChatCompletionNewParams{
		Model: openaisdk.ChatModel(model),
		Messages: []openaisdk.ChatCompletionMessageParamUnion{
			openaisdk.SystemMessage(systemPrompt),
			openaisdk.UserMessage(userPrompt),
		},
		ResponseFormat: openaisdk.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openaisdk.ResponseFormatJSONSchemaParam{JSONSchema: schemaParam},
		},
		Temperature: openaisdk.Float(0),
	})
	if err != nil {
		return nil, fmt.Errorf("chat.completions.new: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("openai translation: empty choices in response")
	}

	raw := resp.Choices[0].Message.Content
	parsed, err := parseTranslationJSON(raw)
	if err != nil {
		log.WithError(err).WithField("raw", truncate(raw, 256)).Warn("openai translation: failed to parse JSON response")
		return nil, fmt.Errorf("failed to parse translation JSON: %w", err)
	}

	translations := make(map[string]string, len(targetLangs))
	for _, l := range targetLangs {
		translations[l] = parsed[l]
	}

	return &plugnmeet.InsightsTextTranslationResult{
		SourceText:   text,
		SourceLang:   sourceLang,
		Translations: translations,
	}, nil
}

// buildTranslationSchema produces a JSON Schema requiring exactly the supplied
// language codes as string properties. Strict mode then forces the model to
// emit all of them and only them.
func buildTranslationSchema(targetLangs []string) map[string]any {
	properties := make(map[string]any, len(targetLangs))
	required := make([]string, 0, len(targetLangs))
	for _, l := range targetLangs {
		properties[l] = map[string]any{"type": "string"}
		required = append(required, l)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

// parseTranslationJSON unmarshals the response, retrying once with markdown
// fences stripped for backends that ignore Strict mode.
func parseTranslationJSON(raw string) (map[string]string, error) {
	parsed := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
		return parsed, nil
	} else if cleaned := stripJSONNoise(raw); cleaned != raw {
		if err2 := json.Unmarshal([]byte(cleaned), &parsed); err2 == nil {
			return parsed, nil
		}
		return nil, err
	} else {
		return nil, err
	}
}

// stripJSONNoise trims markdown code fences and surrounding whitespace from
// a model response. Strict-mode-compliant backends shouldn't need this; it's
// purely a forgiveness layer for OpenAI-compatible servers that don't enforce
// the schema.
func stripJSONNoise(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
