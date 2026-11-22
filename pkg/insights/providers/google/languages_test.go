package google

import (
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func TestNormalizeTranscribeLang(t *testing.T) {
	tests := map[string]string{
		"":           "",
		"en":         "en-US",
		"EN":         "en-US",
		"en-us":      "en-US",
		"en-GB":      "en-GB",
		"en_IN":      "en-IN",
		"ja-JP":      "ja-JP",
		"ja":         "ja-JP",
		"ar-SA":      "ar-EG",
		"es-ES":      "es-419",
		"es":         "es-419",
		"pt":         "pt-BR",
		"pt-PT":      "pt-PT",
		"bn":         "bn-BD",
		"bn-IN":      "bn-IN",
		"zh":         "cmn-Hans-CN",
		"zh-CN":      "cmn-Hans-CN",
		"zh-TW":      "yue-Hant-HK",
		"no-NO":      "nb-NO",
		"sw":         "sw-KE",
		"ceb":        "ceb",
		"xx":         "",
		"xx-YY":      "",
		"not a lang": "",
	}

	for input, want := range tests {
		if got := normalizeTranscribeLang(input); got != want {
			t.Errorf("normalizeTranscribeLang(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTranscribeLanguageCodes(t *testing.T) {
	if got := transcribeLanguageCodes(""); len(got) != 0 {
		t.Fatalf("transcribeLanguageCodes(\"\") = %v, want nil", got)
	}
	if got := transcribeLanguageCodes("unknown"); len(got) != 0 {
		t.Fatalf("transcribeLanguageCodes(unknown) = %v, want nil", got)
	}

	got := transcribeLanguageCodes("de")
	if len(got) != 1 || got[0] != "de-DE" {
		t.Fatalf("transcribeLanguageCodes(de) = %v, want [de-DE]", got)
	}
}

func TestGetSupportedLanguages(t *testing.T) {
	provider := newTestProvider()

	for _, serviceType := range []insights.ServiceType{
		insights.ServiceTypeTranscription,
		insights.ServiceTypeTranslation,
		insights.ServiceTypeSpeechSynthesis,
	} {
		langs := provider.GetSupportedLanguages(serviceType)
		if len(langs) == 0 {
			t.Fatalf("GetSupportedLanguages(%q) returned no languages", serviceType)
		}

		for _, lang := range langs {
			if lang.Code == "" || lang.Name == "" || lang.Locale == "" {
				t.Fatalf("GetSupportedLanguages(%q) returned an incomplete entry: %+v", serviceType, lang)
			}
		}
	}

	if langs := provider.GetSupportedLanguages(insights.ServiceTypeAITextChat); len(langs) != 0 {
		t.Fatalf("GetSupportedLanguages(ai_text_chat) = %v, want an empty list", langs)
	}
}

// TestTranslationCoversEveryTranscriptionLocale mirrors the a-client
// getSubtitleLangs contract: the locale of every spoken language is looked up
// as a translation code, so a missing entry hides that language entirely.
func TestTranslationCoversEveryTranscriptionLocale(t *testing.T) {
	provider := newTestProvider()

	translationCodes := make(map[string]struct{})
	for _, lang := range provider.GetSupportedLanguages(insights.ServiceTypeTranslation) {
		translationCodes[lang.Code] = struct{}{}
	}

	for _, lang := range provider.GetSupportedLanguages(insights.ServiceTypeTranscription) {
		if _, ok := translationCodes[lang.Locale]; !ok {
			t.Errorf("transcription locale %q (from %q) has no translation entry", lang.Locale, lang.Code)
		}
	}
}

func TestGetLocaleFromCode(t *testing.T) {
	tests := map[string]string{
		"en-US":       "en",
		"en-us":       "en",
		"bn-BD":       "bn",
		"es-419":      "es",
		"es-US":       "es",
		"cmn-Hans-CN": "cmn",
		"en":          "en",
		"EN":          "en",
		"en_IN":       "en",
		"zh":          "cmn",
		"no":          "nb",
		"tl":          "fil",
		"ceb":         "ceb",
		"xx-YY":       "xx",
		"xx":          "xx",
		"":            "",
	}

	for input, want := range tests {
		if got := GetLocaleFromCode(input); got != want {
			t.Errorf("GetLocaleFromCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetSupportedLanguagesReturnsCopies(t *testing.T) {
	provider := newTestProvider()

	first := provider.GetSupportedLanguages(insights.ServiceTypeTranscription)
	first[0].Code = "mutated"

	second := provider.GetSupportedLanguages(insights.ServiceTypeTranscription)
	if second[0].Code == "mutated" {
		t.Fatal("GetSupportedLanguages must return fresh entries")
	}
}
