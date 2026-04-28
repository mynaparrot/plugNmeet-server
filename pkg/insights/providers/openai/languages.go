package openai

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// supportedLanguages enumerates the languages we surface for transcription
// and translation. The transcription set tracks Whisper's documented
// coverage; the translation set is the same superset since modern chat
// models (gpt-4o, gpt-4o-mini, Llama-class instruct models) translate
// between any of these pairs comfortably.
var supportedLanguages = map[insights.ServiceType][]plugnmeet.InsightsSupportedLangInfo{
	insights.ServiceTypeTranscription: whisperLanguages(),
	insights.ServiceTypeTranslation:   whisperLanguages(),
}

func whisperLanguages() []plugnmeet.InsightsSupportedLangInfo {
	return []plugnmeet.InsightsSupportedLangInfo{
		{Code: "af", Name: "Afrikaans", Locale: "af"},
		{Code: "ar", Name: "Arabic", Locale: "ar"},
		{Code: "az", Name: "Azerbaijani", Locale: "az"},
		{Code: "be", Name: "Belarusian", Locale: "be"},
		{Code: "bg", Name: "Bulgarian", Locale: "bg"},
		{Code: "bn", Name: "Bengali", Locale: "bn"},
		{Code: "bs", Name: "Bosnian", Locale: "bs"},
		{Code: "ca", Name: "Catalan", Locale: "ca"},
		{Code: "cs", Name: "Czech", Locale: "cs"},
		{Code: "cy", Name: "Welsh", Locale: "cy"},
		{Code: "da", Name: "Danish", Locale: "da"},
		{Code: "de", Name: "German", Locale: "de"},
		{Code: "el", Name: "Greek", Locale: "el"},
		{Code: "en", Name: "English", Locale: "en"},
		{Code: "es", Name: "Spanish", Locale: "es"},
		{Code: "et", Name: "Estonian", Locale: "et"},
		{Code: "fa", Name: "Persian", Locale: "fa"},
		{Code: "fi", Name: "Finnish", Locale: "fi"},
		{Code: "fr", Name: "French", Locale: "fr"},
		{Code: "gl", Name: "Galician", Locale: "gl"},
		{Code: "he", Name: "Hebrew", Locale: "he"},
		{Code: "hi", Name: "Hindi", Locale: "hi"},
		{Code: "hr", Name: "Croatian", Locale: "hr"},
		{Code: "hu", Name: "Hungarian", Locale: "hu"},
		{Code: "hy", Name: "Armenian", Locale: "hy"},
		{Code: "id", Name: "Indonesian", Locale: "id"},
		{Code: "is", Name: "Icelandic", Locale: "is"},
		{Code: "it", Name: "Italian", Locale: "it"},
		{Code: "ja", Name: "Japanese", Locale: "ja"},
		{Code: "kk", Name: "Kazakh", Locale: "kk"},
		{Code: "kn", Name: "Kannada", Locale: "kn"},
		{Code: "ko", Name: "Korean", Locale: "ko"},
		{Code: "lt", Name: "Lithuanian", Locale: "lt"},
		{Code: "lv", Name: "Latvian", Locale: "lv"},
		{Code: "mi", Name: "Maori", Locale: "mi"},
		{Code: "mk", Name: "Macedonian", Locale: "mk"},
		{Code: "mr", Name: "Marathi", Locale: "mr"},
		{Code: "ms", Name: "Malay", Locale: "ms"},
		{Code: "ne", Name: "Nepali", Locale: "ne"},
		{Code: "nl", Name: "Dutch", Locale: "nl"},
		{Code: "no", Name: "Norwegian", Locale: "no"},
		{Code: "pl", Name: "Polish", Locale: "pl"},
		{Code: "pt", Name: "Portuguese", Locale: "pt"},
		{Code: "ro", Name: "Romanian", Locale: "ro"},
		{Code: "ru", Name: "Russian", Locale: "ru"},
		{Code: "sk", Name: "Slovak", Locale: "sk"},
		{Code: "sl", Name: "Slovenian", Locale: "sl"},
		{Code: "sr", Name: "Serbian", Locale: "sr"},
		{Code: "sv", Name: "Swedish", Locale: "sv"},
		{Code: "sw", Name: "Swahili", Locale: "sw"},
		{Code: "ta", Name: "Tamil", Locale: "ta"},
		{Code: "th", Name: "Thai", Locale: "th"},
		{Code: "tl", Name: "Tagalog", Locale: "tl"},
		{Code: "tr", Name: "Turkish", Locale: "tr"},
		{Code: "uk", Name: "Ukrainian", Locale: "uk"},
		{Code: "ur", Name: "Urdu", Locale: "ur"},
		{Code: "vi", Name: "Vietnamese", Locale: "vi"},
		{Code: "zh", Name: "Chinese", Locale: "zh"},
	}
}
