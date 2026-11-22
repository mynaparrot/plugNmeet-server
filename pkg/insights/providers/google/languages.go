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

// translationLanguages holds one entry per Locale of transcribeLanguages, keyed
// by that same short locale. Clients translate a spoken language into its
// locale (e.g. "en-US" -> "en") and then look the locale up as a Code here, so
// every Locale of transcribeLanguages must exist as a Code in this list.
var translationLanguages = []plugnmeet.InsightsSupportedLangInfo{
	{Code: "af", Name: "Afrikaans", Locale: "af"},
	{Code: "am", Name: "Amharic", Locale: "am"},
	{Code: "ar", Name: "Arabic", Locale: "ar"},
	{Code: "hy", Name: "Armenian", Locale: "hy"},
	{Code: "rup", Name: "Aromanian", Locale: "rup"},
	{Code: "as", Name: "Assamese", Locale: "as"},
	{Code: "az", Name: "Azerbaijani", Locale: "az"},
	{Code: "be", Name: "Belarusian", Locale: "be"},
	{Code: "bn", Name: "Bengali", Locale: "bn"},
	{Code: "bs", Name: "Bosnian", Locale: "bs"},
	{Code: "bg", Name: "Bulgarian", Locale: "bg"},
	{Code: "my", Name: "Burmese", Locale: "my"},
	{Code: "yue", Name: "Cantonese", Locale: "yue"},
	{Code: "ca", Name: "Catalan", Locale: "ca"},
	{Code: "ceb", Name: "Cebuano", Locale: "ceb"},
	{Code: "hr", Name: "Croatian", Locale: "hr"},
	{Code: "cs", Name: "Czech", Locale: "cs"},
	{Code: "da", Name: "Danish", Locale: "da"},
	{Code: "nl", Name: "Dutch", Locale: "nl"},
	{Code: "en", Name: "English", Locale: "en"},
	{Code: "et", Name: "Estonian", Locale: "et"},
	{Code: "fa", Name: "Farsi", Locale: "fa"},
	{Code: "fil", Name: "Filipino", Locale: "fil"},
	{Code: "fi", Name: "Finnish", Locale: "fi"},
	{Code: "fr", Name: "French", Locale: "fr"},
	{Code: "gl", Name: "Galician", Locale: "gl"},
	{Code: "ka", Name: "Georgian", Locale: "ka"},
	{Code: "de", Name: "German", Locale: "de"},
	{Code: "el", Name: "Greek", Locale: "el"},
	{Code: "gu", Name: "Gujarati", Locale: "gu"},
	{Code: "ha", Name: "Hausa", Locale: "ha"},
	{Code: "he", Name: "Hebrew", Locale: "he"},
	{Code: "hi", Name: "Hindi", Locale: "hi"},
	{Code: "hu", Name: "Hungarian", Locale: "hu"},
	{Code: "is", Name: "Icelandic", Locale: "is"},
	{Code: "id", Name: "Indonesian", Locale: "id"},
	{Code: "it", Name: "Italian", Locale: "it"},
	{Code: "ja", Name: "Japanese", Locale: "ja"},
	{Code: "jv", Name: "Javanese", Locale: "jv"},
	{Code: "kea", Name: "Kabuverdianu", Locale: "kea"},
	{Code: "kn", Name: "Kannada", Locale: "kn"},
	{Code: "kk", Name: "Kazakh", Locale: "kk"},
	{Code: "km", Name: "Khmer", Locale: "km"},
	{Code: "ko", Name: "Korean", Locale: "ko"},
	{Code: "ky", Name: "Kyrgyz", Locale: "ky"},
	{Code: "lv", Name: "Latvian", Locale: "lv"},
	{Code: "ln", Name: "Lingala", Locale: "ln"},
	{Code: "lt", Name: "Lithuanian", Locale: "lt"},
	{Code: "mk", Name: "Macedonian", Locale: "mk"},
	{Code: "ms", Name: "Malay", Locale: "ms"},
	{Code: "ml", Name: "Malayalam", Locale: "ml"},
	{Code: "mt", Name: "Maltese", Locale: "mt"},
	{Code: "cmn", Name: "Mandarin Chinese", Locale: "cmn"},
	{Code: "mr", Name: "Marathi", Locale: "mr"},
	{Code: "mn", Name: "Mongolian", Locale: "mn"},
	{Code: "ne", Name: "Nepali", Locale: "ne"},
	{Code: "nb", Name: "Norwegian", Locale: "nb"},
	{Code: "or", Name: "Oriya", Locale: "or"},
	{Code: "pl", Name: "Polish", Locale: "pl"},
	{Code: "pt", Name: "Portuguese", Locale: "pt"},
	{Code: "pa", Name: "Punjabi", Locale: "pa"},
	{Code: "ro", Name: "Romanian", Locale: "ro"},
	{Code: "ru", Name: "Russian", Locale: "ru"},
	{Code: "sr", Name: "Serbian", Locale: "sr"},
	{Code: "sd", Name: "Sindhi", Locale: "sd"},
	{Code: "sk", Name: "Slovak", Locale: "sk"},
	{Code: "sl", Name: "Slovenian", Locale: "sl"},
	{Code: "es", Name: "Spanish", Locale: "es"},
	{Code: "sw", Name: "Swahili", Locale: "sw"},
	{Code: "sv", Name: "Swedish", Locale: "sv"},
	{Code: "tg", Name: "Tajik", Locale: "tg"},
	{Code: "te", Name: "Telugu", Locale: "te"},
	{Code: "th", Name: "Thai", Locale: "th"},
	{Code: "tr", Name: "Turkish", Locale: "tr"},
	{Code: "uk", Name: "Ukrainian", Locale: "uk"},
	{Code: "uz", Name: "Uzbek", Locale: "uz"},
	{Code: "vi", Name: "Vietnamese", Locale: "vi"},
}

// ttsLanguages shares the translation codes: Gemini TTS detects the input
// language itself and supports 130+ languages, so the codes only drive the
// language selection in the UI.
// Source: https://ai.google.dev/gemini-api/docs/speech-generation#languages
var ttsLanguages = translationLanguages

// supportedLanguages holds the static language list per service.
var supportedLanguages = map[insights.ServiceType][]plugnmeet.InsightsSupportedLangInfo{
	insights.ServiceTypeTranscription:   transcribeLanguages,
	insights.ServiceTypeTranslation:     translationLanguages,
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
	transcribeLangByCode       = make(map[string]string, len(transcribeLanguages))
	transcribeLangByPrimary    = make(map[string]string, len(transcribeLanguages))
	transcribeLangLocaleByCode = make(map[string]string, len(transcribeLanguages))
)

func init() {
	for i := range transcribeLanguages {
		code, locale := transcribeLanguages[i].Code, transcribeLanguages[i].Locale
		lowerCode := strings.ToLower(code)
		transcribeLangByCode[lowerCode] = code
		transcribeLangLocaleByCode[lowerCode] = locale
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

// GetLocaleFromCode resolves a BCP-47 code or alias to the short locale that
// plugNmeet clients use (e.g. "en-US" -> "en", "es-419" -> "es",
// "cmn-Hans-CN" -> "cmn"). Unknown codes fall back to their primary subtag and
// finally to the trimmed input.
func GetLocaleFromCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	if canonical := normalizeTranscribeLang(code); canonical != "" {
		return transcribeLangLocaleByCode[strings.ToLower(canonical)]
	}
	if primary, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(code, "_", "-")), "-"); primary != "" {
		return primary
	}
	return code
}
