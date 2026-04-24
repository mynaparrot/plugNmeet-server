package local

import (
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// supportedLanguages lists languages supported by faster-whisper (transcription)
// and NLLB-200 (translation). These are common language codes used in PlugNmeet.
var supportedLanguages = map[insights.ServiceType][]plugnmeet.InsightsSupportedLangInfo{
	insights.ServiceTypeTranscription: {
		{Code: "de", Name: "German", Locale: "de"},
		{Code: "en", Name: "English", Locale: "en"},
		{Code: "ar", Name: "Arabic", Locale: "ar"},
		{Code: "uk", Name: "Ukrainian", Locale: "uk"},
		{Code: "ru", Name: "Russian", Locale: "ru"},
		{Code: "fr", Name: "French", Locale: "fr"},
		{Code: "es", Name: "Spanish", Locale: "es"},
		{Code: "it", Name: "Italian", Locale: "it"},
		{Code: "pl", Name: "Polish", Locale: "pl"},
		{Code: "tr", Name: "Turkish", Locale: "tr"},
		{Code: "fa", Name: "Persian", Locale: "fa"},
		{Code: "zh", Name: "Chinese", Locale: "zh"},
		{Code: "ja", Name: "Japanese", Locale: "ja"},
		{Code: "ko", Name: "Korean", Locale: "ko"},
		{Code: "pt", Name: "Portuguese", Locale: "pt"},
		{Code: "nl", Name: "Dutch", Locale: "nl"},
	},
	insights.ServiceTypeTranslation: {
		{Code: "de", Name: "German", Locale: "de"},
		{Code: "en", Name: "English", Locale: "en"},
		{Code: "ar", Name: "Arabic", Locale: "ar"},
		{Code: "uk", Name: "Ukrainian", Locale: "uk"},
		{Code: "ru", Name: "Russian", Locale: "ru"},
		{Code: "fr", Name: "French", Locale: "fr"},
		{Code: "es", Name: "Spanish", Locale: "es"},
		{Code: "it", Name: "Italian", Locale: "it"},
		{Code: "pl", Name: "Polish", Locale: "pl"},
		{Code: "tr", Name: "Turkish", Locale: "tr"},
		{Code: "fa", Name: "Persian", Locale: "fa"},
		{Code: "zh", Name: "Chinese", Locale: "zh"},
		{Code: "ja", Name: "Japanese", Locale: "ja"},
		{Code: "ko", Name: "Korean", Locale: "ko"},
		{Code: "pt", Name: "Portuguese", Locale: "pt"},
		{Code: "nl", Name: "Dutch", Locale: "nl"},
	},
}
