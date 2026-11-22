package google

import (
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// transcribeLanguages is the official BCP-47 list supported by Gemini 3.5
// Transcribe. Source: https://ai.google.dev/gemini-api/docs/transcribe#supported-languages
// Entries of a language are ordered by preference, so the first one wins when a
// caller only sends the short code (e.g. "en" -> "en-US").
var transcribeLanguages = []plugnmeet.InsightsSupportedLangInfo{
	{Code: "af-ZA", Name: "Afrikaans", Locale: "af"},
	{Code: "am-ET", Name: "Amharic", Locale: "am"},
	{Code: "ar-EG", Name: "Arabic (Egypt)", Locale: "ar"},
	{Code: "hy-AM", Name: "Armenian", Locale: "hy"},
	{Code: "as-IN", Name: "Assamese", Locale: "as"},
	{Code: "az-AZ", Name: "Azerbaijani", Locale: "az"},
	{Code: "be-BY", Name: "Belarusian", Locale: "be"},
	{Code: "bn-BD", Name: "Bengali (Bangladesh)", Locale: "bn"},
	{Code: "bn-IN", Name: "Bengali (India)", Locale: "bn"},
	{Code: "bs-BA", Name: "Bosnian", Locale: "bs"},
	{Code: "bg-BG", Name: "Bulgarian", Locale: "bg"},
	{Code: "rup-BG", Name: "Bulgarian (Aromanian)", Locale: "rup"},
	{Code: "my-MM", Name: "Burmese", Locale: "my"},
	{Code: "yue-Hant-HK", Name: "Cantonese (Traditional)", Locale: "yue"},
	{Code: "ca-ES", Name: "Catalan", Locale: "ca"},
	{Code: "ceb", Name: "Cebuano", Locale: "ceb"},
	{Code: "km-KH", Name: "Central Khmer", Locale: "km"},
	{Code: "hr-HR", Name: "Croatian", Locale: "hr"},
	{Code: "cs-CZ", Name: "Czech", Locale: "cs"},
	{Code: "da-DK", Name: "Danish", Locale: "da"},
	{Code: "nl-NL", Name: "Dutch", Locale: "nl"},
	{Code: "en-US", Name: "English (United States)", Locale: "en"},
	{Code: "en-GB", Name: "English (Great Britain)", Locale: "en"},
	{Code: "en-IN", Name: "English (India)", Locale: "en"},
	{Code: "et-EE", Name: "Estonian", Locale: "et"},
	{Code: "fa-IR", Name: "Farsi", Locale: "fa"},
	{Code: "fil-PH", Name: "Filipino", Locale: "fil"},
	{Code: "fi-FI", Name: "Finnish", Locale: "fi"},
	{Code: "fr-FR", Name: "French", Locale: "fr"},
	{Code: "gl-ES", Name: "Galician", Locale: "gl"},
	{Code: "ka-GE", Name: "Georgian", Locale: "ka"},
	{Code: "de-DE", Name: "German", Locale: "de"},
	{Code: "el-GR", Name: "Greek", Locale: "el"},
	{Code: "gu-IN", Name: "Gujarati", Locale: "gu"},
	{Code: "ha-NG", Name: "Hausa", Locale: "ha"},
	{Code: "he-IL", Name: "Hebrew", Locale: "he"},
	{Code: "hi-IN", Name: "Hindi", Locale: "hi"},
	{Code: "hu-HU", Name: "Hungarian", Locale: "hu"},
	{Code: "is-IS", Name: "Icelandic", Locale: "is"},
	{Code: "id-ID", Name: "Indonesian", Locale: "id"},
	{Code: "it-IT", Name: "Italian", Locale: "it"},
	{Code: "ja-JP", Name: "Japanese", Locale: "ja"},
	{Code: "jv-ID", Name: "Javanese", Locale: "jv"},
	{Code: "kea-CV", Name: "Kabuverdianu", Locale: "kea"},
	{Code: "kn-IN", Name: "Kannada", Locale: "kn"},
	{Code: "kk-KZ", Name: "Kazakh", Locale: "kk"},
	{Code: "ko-KR", Name: "Korean", Locale: "ko"},
	{Code: "ky-KG", Name: "Kyrgyz", Locale: "ky"},
	{Code: "lv-LV", Name: "Latvian", Locale: "lv"},
	{Code: "ln-CD", Name: "Lingala", Locale: "ln"},
	{Code: "lt-LT", Name: "Lithuanian", Locale: "lt"},
	{Code: "mk-MK", Name: "Macedonian", Locale: "mk"},
	{Code: "ms-MY", Name: "Malay", Locale: "ms"},
	{Code: "ml-IN", Name: "Malayalam", Locale: "ml"},
	{Code: "mt-MT", Name: "Maltese", Locale: "mt"},
	{Code: "cmn-Hans-CN", Name: "Mandarin Chinese (Simplified)", Locale: "cmn"},
	{Code: "mr-IN", Name: "Marathi", Locale: "mr"},
	{Code: "mn-MN", Name: "Mongolian", Locale: "mn"},
	{Code: "ne-NP", Name: "Nepali", Locale: "ne"},
	{Code: "nb-NO", Name: "Norwegian", Locale: "nb"},
	{Code: "or-IN", Name: "Oriya", Locale: "or"},
	{Code: "pl-PL", Name: "Polish", Locale: "pl"},
	{Code: "pt-BR", Name: "Portuguese (Brazil)", Locale: "pt"},
	{Code: "pt-PT", Name: "Portuguese (Portugal)", Locale: "pt"},
	{Code: "pa-IN", Name: "Punjabi", Locale: "pa"},
	{Code: "pa-Guru-IN", Name: "Punjabi (Gurmukhi script)", Locale: "pa"},
	{Code: "ro-RO", Name: "Romanian", Locale: "ro"},
	{Code: "ru-RU", Name: "Russian", Locale: "ru"},
	{Code: "sr-RS", Name: "Serbian", Locale: "sr"},
	{Code: "sd-Arab-IN", Name: "Sindhi (Arabic script)", Locale: "sd"},
	{Code: "sk-SK", Name: "Slovak", Locale: "sk"},
	{Code: "sl-SI", Name: "Slovenian", Locale: "sl"},
	{Code: "es-419", Name: "Spanish (Latin America)", Locale: "es"},
	{Code: "es-US", Name: "Spanish (United States)", Locale: "es"},
	{Code: "sw-KE", Name: "Swahili (Kenya)", Locale: "sw"},
	{Code: "sv-SE", Name: "Swedish", Locale: "sv"},
	{Code: "tg-TJ", Name: "Tajik", Locale: "tg"},
	{Code: "te-IN", Name: "Telugu", Locale: "te"},
	{Code: "th-TH", Name: "Thai", Locale: "th"},
	{Code: "tr-TR", Name: "Turkish", Locale: "tr"},
	{Code: "uk-UA", Name: "Ukrainian", Locale: "uk"},
	{Code: "uz-UZ", Name: "Uzbek", Locale: "uz"},
	{Code: "vi-VN", Name: "Vietnamese", Locale: "vi"},
}

// ttsLanguages shares the transcription locales: Gemini TTS detects the input
// language itself and supports 130+ languages, so the codes are only used to
// key `voice-<lang>` mappings and language selection in the UI.
// Source: https://ai.google.dev/gemini-api/docs/speech-generation#languages
var ttsLanguages = transcribeLanguages

// supportedLanguages holds the static language list per service.
var supportedLanguages = map[insights.ServiceType][]plugnmeet.InsightsSupportedLangInfo{
	insights.ServiceTypeTranscription:   transcribeLanguages,
	insights.ServiceTypeTranslation:     transcribeLanguages,
	insights.ServiceTypeSpeechSynthesis: ttsLanguages,
}

// transcribeLangAliases maps codes used by plugNmeet clients onto the closest
// code of Gemini's list when the short form is not enough to pick one.
var transcribeLangAliases = map[string]string{
	"zh":      "cmn-Hans-CN",
	"zh-cn":   "cmn-Hans-CN",
	"zh-hans": "cmn-Hans-CN",
	"zh-sg":   "cmn-Hans-CN",
	"zh-tw":   "yue-Hant-HK",
	"zh-hk":   "yue-Hant-HK",
	"zh-hant": "yue-Hant-HK",
	"no":      "nb-NO",
	"no-no":   "nb-NO",
	"tl":      "fil-PH",
}

var (
	transcribeLangByCode    = make(map[string]string, len(transcribeLanguages))
	transcribeLangByPrimary = make(map[string]string, len(transcribeLanguages))
)

func init() {
	for i := range transcribeLanguages {
		code, locale := transcribeLanguages[i].Code, transcribeLanguages[i].Locale
		transcribeLangByCode[strings.ToLower(code)] = code
		// The list is ordered by preference, so the first locale of a language wins.
		if _, ok := transcribeLangByPrimary[locale]; !ok {
			transcribeLangByPrimary[locale] = code
		}
	}
}

// normalizeTranscribeLang resolves a spoken language code to a BCP-47 code of
// Gemini's list. It returns "" for an unknown language so the caller can rely
// on automatic detection instead of sending a code the API may reject.
func normalizeTranscribeLang(lang string) string {
	code := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
	if code == "" {
		return ""
	}
	if canonical, ok := transcribeLangByCode[code]; ok {
		return canonical
	}
	if canonical, ok := transcribeLangAliases[code]; ok {
		return canonical
	}
	if primary, _, _ := strings.Cut(code, "-"); primary != "" {
		if canonical, ok := transcribeLangByPrimary[primary]; ok {
			return canonical
		}
	}
	return ""
}

// transcribeLanguageCodes builds the LanguageCodes hint of the live config.
func transcribeLanguageCodes(lang string) []string {
	if code := normalizeTranscribeLang(lang); code != "" {
		return []string{code}
	}
	return nil
}
