package google

import (
	"context"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func TestResolveTranslateModel(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		want    string
	}{
		{
			name: "defaults without a service config",
			want: defaultTranslateModel,
		},
		{
			name:    "uses an explicit translate model",
			options: map[string]interface{}{"translate_model": "gemini-3.5-flash-lite", "model": "gemini-3.1-pro"},
			want:    "gemini-3.5-flash-lite",
		},
		{
			name:    "uses the generic model option for text models",
			options: map[string]interface{}{"model": "gemini-3.7-flash"},
			want:    "gemini-3.7-flash",
		},
		{
			name:    "never uses the transcription model",
			options: map[string]interface{}{"model": "gemini-3.5-transcribe-live"},
			want:    defaultTranslateModel,
		},
		{
			name:    "never uses a batch transcription model",
			options: map[string]interface{}{"model": "gemini-3.5-transcribe"},
			want:    defaultTranslateModel,
		},
		{
			name:    "never uses the tts model",
			options: map[string]interface{}{"model": "gemini-3.8-flash-tts"},
			want:    defaultTranslateModel,
		},
		{
			name:    "never uses a live model",
			options: map[string]interface{}{"model": "gemini-3.8-live"},
			want:    defaultTranslateModel,
		},
		{
			name:    "prefers the translate model over a speech model",
			options: map[string]interface{}{"model": "gemini-3.8-flash-tts", "translate_model": "gemini-3.1-flash-lite"},
			want:    "gemini-3.1-flash-lite",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var service *config.ServiceConfig
			if tt.options != nil {
				service = &config.ServiceConfig{Options: tt.options}
			}

			if got := resolveTranslateModel(service); got != tt.want {
				t.Fatalf("resolveTranslateModel() = %q, want %q", got, tt.want)
			}
		})
	}

	if defaultTranslateModel != "gemini-3.1-flash-lite" {
		t.Fatalf("default model = %q, want gemini-3.1-flash-lite", defaultTranslateModel)
	}
}

func TestCleanTargetLangs(t *testing.T) {
	got := cleanTargetLangs([]string{"es-419", " fr-FR ", "es-419", "", "ES-419"})
	want := []string{"es-419", "fr-FR"}

	if len(got) != len(want) {
		t.Fatalf("cleanTargetLangs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cleanTargetLangs() = %v, want %v", got, want)
		}
	}
}

func TestTranslateTextSkipsEmptyInput(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		targets []string
	}{
		{name: "blank text", text: "   ", targets: []string{"es-419"}},
		{name: "no target language", text: "hello", targets: nil},
		{name: "blank targets", text: "hello", targets: []string{" "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil client proves no API call is made.
			result, err := translateText(context.Background(), nil, nil, tt.text, "en", tt.targets)
			if err != nil {
				t.Fatalf("translateText() error = %v", err)
			}
			if result == nil {
				t.Fatal("translateText() = nil, want an empty result")
			}
			if len(result.Translations) != 0 {
				t.Fatalf("translations = %v, want none", result.Translations)
			}
		})
	}
}

func TestParseTranslations(t *testing.T) {
	t.Run("keeps only the requested languages", func(t *testing.T) {
		raw := `{"es-419":"buenos días","fr-FR":"bonjour","de-DE":"guten Tag"}`

		got, err := parseTranslations(raw, []string{"es-419", "fr-FR"})
		if err != nil {
			t.Fatalf("parseTranslations() error = %v", err)
		}
		if len(got) != 2 || got["es-419"] != "buenos días" || got["fr-FR"] != "bonjour" {
			t.Fatalf("translations = %v", got)
		}
	})

	t.Run("matches keys case-insensitively", func(t *testing.T) {
		raw := `{"ES-419":"buenos días","fr-fr":"bonjour"}`

		got, err := parseTranslations(raw, []string{"es-419", "fr-FR"})
		if err != nil {
			t.Fatalf("parseTranslations() error = %v", err)
		}
		if got["es-419"] != "buenos días" || got["fr-FR"] != "bonjour" {
			t.Fatalf("translations = %v", got)
		}
	})

	t.Run("strips a markdown fence", func(t *testing.T) {
		raw := "```json\n{\"ja-JP\":\"こんにちは\"}\n```"

		got, err := parseTranslations(raw, []string{"ja-JP"})
		if err != nil {
			t.Fatalf("parseTranslations() error = %v", err)
		}
		if got["ja-JP"] != "こんにちは" {
			t.Fatalf("translations = %v", got)
		}
	})

	t.Run("handles the translated_text fallback shape", func(t *testing.T) {
		raw := `{"language_code":"es-419","translated_text":"buenos días"}`

		got, err := parseTranslations(raw, []string{"es-419"})
		if err != nil {
			t.Fatalf("parseTranslations() error = %v", err)
		}
		if got["es-419"] != "buenos días" {
			t.Fatalf("translations = %v", got)
		}
	})

	t.Run("reports missing target languages", func(t *testing.T) {
		if _, err := parseTranslations(`{"it-IT":"ciao"}`, []string{"es-419"}); err == nil {
			t.Fatal("parseTranslations() error = nil, want a missing translation error")
		}
	})

	t.Run("reports invalid json", func(t *testing.T) {
		if _, err := parseTranslations("not json", []string{"es-419"}); err == nil {
			t.Fatal("parseTranslations() error = nil, want a parse error")
		}
	})
}

func TestBuildTranslatePrompt(t *testing.T) {
	prompt := buildTranslatePrompt("hello", "en", []string{"es-419", "fr-FR"})

	for _, want := range []string{"Translate from en", "es-419, fr-FR", "hello"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt %q misses %q", prompt, want)
		}
	}

	if !strings.Contains(buildTranslatePrompt("hello", "", []string{"es"}), "the detected language") {
		t.Error("prompt must name the detected language when the source is unknown")
	}
}
