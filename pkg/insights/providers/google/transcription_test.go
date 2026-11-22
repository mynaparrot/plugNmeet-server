package google

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/sirupsen/logrus"
	"google.golang.org/genai"
)

func newTestProvider() *GoogleProvider {
	return &GoogleProvider{logger: logrus.NewEntry(logrus.New())}
}

// readEvent returns the next event or fails the test on a closed channel.
func readEvent(t *testing.T, ch <-chan *insights.TranscriptionEvent) *insights.TranscriptionEvent {
	t.Helper()

	select {
	case event, ok := <-ch:
		if !ok {
			t.Fatal("results channel closed unexpectedly")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a transcription event")
		return nil
	}
}

func TestBuildTranscriptionConfig(t *testing.T) {
	tests := []struct {
		name      string
		spoken    string
		mode      string
		wantMode  genai.AudioTranscriptionConfigMode
		wantCodes []string
	}{
		{
			name:     "defaults to verbatim without a language hint",
			mode:     string(genai.AudioTranscriptionConfigModeVerbatim),
			wantMode: genai.AudioTranscriptionConfigModeVerbatim,
		},
		{
			name:      "maps a short code to the BCP-47 default",
			spoken:    "en",
			mode:      "VERBATIM",
			wantMode:  genai.AudioTranscriptionConfigModeVerbatim,
			wantCodes: []string{"en-US"},
		},
		{
			name:      "keeps a supported BCP-47 code",
			spoken:    "ja-JP",
			mode:      "VERBATIM",
			wantMode:  genai.AudioTranscriptionConfigModeVerbatim,
			wantCodes: []string{"ja-JP"},
		},
		{
			name:      "maps a locale that Gemini does not list to the language default",
			spoken:    "es-ES",
			mode:      "VERBATIM",
			wantMode:  genai.AudioTranscriptionConfigModeVerbatim,
			wantCodes: []string{"es-419"},
		},
		{
			name:     "drops an unknown language so detection takes over",
			spoken:   "xx-YY",
			mode:     "VERBATIM",
			wantMode: genai.AudioTranscriptionConfigModeVerbatim,
		},
		{
			name:      "accepts the smart mode in any case",
			spoken:    "bn",
			mode:      "smart",
			wantMode:  genai.AudioTranscriptionConfigModeSmart,
			wantCodes: []string{"bn-BD"},
		},
		{
			name:     "falls back to verbatim for an unknown mode",
			mode:     "typo",
			wantMode: genai.AudioTranscriptionConfigModeVerbatim,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := buildTranscriptionConfig(&insights.TranscriptionOptions{SpokenLang: tt.spoken}, tt.mode)

			if len(cfg.ResponseModalities) != 1 || cfg.ResponseModalities[0] != genai.ModalityText {
				t.Fatalf("unexpected response modalities: %v", cfg.ResponseModalities)
			}
			if cfg.InputAudioTranscription == nil {
				t.Fatal("input audio transcription must be configured")
			}
			if cfg.InputAudioTranscription.Mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", cfg.InputAudioTranscription.Mode, tt.wantMode)
			}

			got := cfg.InputAudioTranscription.LanguageCodes
			if len(got) != len(tt.wantCodes) {
				t.Fatalf("language codes = %v, want %v", got, tt.wantCodes)
			}
			for i := range got {
				if got[i] != tt.wantCodes[i] {
					t.Fatalf("language codes = %v, want %v", got, tt.wantCodes)
				}
			}
		})
	}
}

func TestCreateTranscriptionDialsWithServiceOptions(t *testing.T) {
	conn := &fakeConn{}
	provider := newTestProvider()
	provider.service = &config.ServiceConfig{Options: map[string]interface{}{
		"model":           "gemini-custom-live",
		"transcribe_mode": "SMART",
	}}

	var gotModel string
	var gotConfig *genai.LiveConnectConfig
	provider.transcribeDial = func(_ context.Context, model string, cfg *genai.LiveConnectConfig) (transcriptionConn, error) {
		gotModel, gotConfig = model, cfg
		return conn, nil
	}

	options := []byte(`{"spokenLang":"de","userName":"user-1"}`)
	stream, err := provider.CreateTranscription(context.Background(), "room-1", "user-1", options)
	if err != nil {
		t.Fatalf("CreateTranscription() error = %v", err)
	}
	defer func() {
		if err := stream.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if gotModel != "gemini-custom-live" {
		t.Errorf("model = %q, want gemini-custom-live", gotModel)
	}
	if gotConfig.InputAudioTranscription.Mode != genai.AudioTranscriptionConfigModeSmart {
		t.Errorf("mode = %q, want SMART", gotConfig.InputAudioTranscription.Mode)
	}
	if codes := gotConfig.InputAudioTranscription.LanguageCodes; len(codes) != 1 || codes[0] != "de-DE" {
		t.Errorf("language codes = %v, want [de-DE]", codes)
	}

	event := readEvent(t, stream.Results())
	if event.Type != insights.EventTypeSessionStarted {
		t.Fatalf("first event = %q, want %q", event.Type, insights.EventTypeSessionStarted)
	}
}

func TestCreateTranscriptionDefaultsModel(t *testing.T) {
	conn := &fakeConn{}
	provider := newTestProvider()

	var gotModel string
	provider.transcribeDial = func(_ context.Context, model string, _ *genai.LiveConnectConfig) (transcriptionConn, error) {
		gotModel = model
		return conn, nil
	}

	stream, err := provider.CreateTranscription(context.Background(), "room-1", "user-1", nil)
	if err != nil {
		t.Fatalf("CreateTranscription() error = %v", err)
	}
	defer func() { _ = stream.Close() }()

	if gotModel != defaultTranscribeLiveModel {
		t.Fatalf("model = %q, want %q", gotModel, defaultTranscribeLiveModel)
	}
	if defaultTranscribeLiveModel != "gemini-3.5-transcribe-live" {
		t.Fatalf("default model = %q, want gemini-3.5-transcribe-live", defaultTranscribeLiveModel)
	}
}

func TestCreateTranscriptionReportsDialError(t *testing.T) {
	provider := newTestProvider()
	provider.transcribeDial = func(context.Context, string, *genai.LiveConnectConfig) (transcriptionConn, error) {
		return nil, errors.New("boom")
	}

	if _, err := provider.CreateTranscription(context.Background(), "room-1", "user-1", nil); err == nil {
		t.Fatal("CreateTranscription() error = nil, want a dial failure")
	}
}

func TestWriteSampleEncodesLittleEndianPCM(t *testing.T) {
	conn := &fakeConn{}
	stream := newTranscriptionStream(context.Background(), conn,
		&insights.TranscriptionOptions{UserName: "user-1"}, newTestProvider(),
		logrus.NewEntry(logrus.New()))
	defer func() { _ = stream.Close() }()

	if err := stream.WriteSample([]int16{1, -2, 3}); err != nil {
		t.Fatalf("WriteSample() error = %v", err)
	}
	if err := stream.WriteSample(nil); err != nil {
		t.Fatalf("WriteSample(nil) error = %v", err)
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	if len(conn.realtime) != 1 {
		t.Fatalf("sent %d chunks, want 1", len(conn.realtime))
	}
	audio := conn.realtime[0].Audio
	if audio == nil {
		t.Fatal("audio blob is missing")
	}
	if audio.MIMEType != "audio/pcm;rate=16000" {
		t.Errorf("mime type = %q, want audio/pcm;rate=16000", audio.MIMEType)
	}
	want := []byte{0x01, 0x00, 0xFE, 0xFF, 0x03, 0x00}
	if !bytes.Equal(audio.Data, want) {
		t.Fatalf("audio = %v, want %v", audio.Data, want)
	}
}

func TestTranscriptionEmitsInterimAndFinalResults(t *testing.T) {
	conn := &fakeConn{
		block: make(chan struct{}),
		incoming: []*genai.LiveServerMessage{
			{ServerContent: &genai.LiveServerContent{
				InterimInputTranscription: &genai.Transcription{Text: " hello ", LanguageCode: "en-US"},
			}},
			{ServerContent: &genai.LiveServerContent{
				InputTranscription: &genai.Transcription{Text: "hello world", LanguageCode: "en-US", Finished: true},
			}},
		},
	}

	opts := &insights.TranscriptionOptions{UserName: "user-1", SpokenLang: "en", AllowedTranscriptionStorage: true}
	stream := newTranscriptionStream(context.Background(), conn, opts, newTestProvider(), logrus.NewEntry(logrus.New()))

	if event := readEvent(t, stream.Results()); event.Type != insights.EventTypeSessionStarted {
		t.Fatalf("first event = %q, want session started", event.Type)
	}

	partial := readEvent(t, stream.Results())
	if partial.Type != insights.EventTypePartialResult || partial.Result == nil {
		t.Fatalf("second event = %+v, want a partial result", partial)
	}
	if partial.Result.Text != "hello" || !partial.Result.IsPartial {
		t.Errorf("partial result = %+v", partial.Result)
	}
	if partial.Result.Lang != "en-US" {
		t.Errorf("partial language = %q, want the detected en-US", partial.Result.Lang)
	}

	final := readEvent(t, stream.Results())
	if final.Type != insights.EventTypeFinalResult || final.Result == nil {
		t.Fatalf("third event = %+v, want a final result", final)
	}
	if final.Result.Text != "hello world" || final.Result.IsPartial {
		t.Errorf("final result = %+v", final.Result)
	}
	if final.Result.FromUserId != "user-1" || final.Result.FromUserName != "user-1" {
		t.Errorf("final result attribution = %+v", final.Result)
	}
	if !final.Result.AllowedTranscriptionStorage {
		t.Error("storage flag was not propagated")
	}
	if len(final.Result.Translations) != 0 {
		t.Errorf("translations = %v, want none", final.Result.Translations)
	}
}

func TestFinalTranscriptTranslatesIntoEveryTargetLanguage(t *testing.T) {
	conn := &fakeConn{
		block: make(chan struct{}),
		incoming: []*genai.LiveServerMessage{
			{ServerContent: &genai.LiveServerContent{
				InputTranscription: &genai.Transcription{Text: "good morning", LanguageCode: "en-US"},
			}},
		},
	}

	provider := newTestProvider()
	var gotText, gotSource string
	var gotTargets []string
	provider.translate = func(_ context.Context, text, sourceLang string, targetLangs []string) (*plugnmeet.InsightsTextTranslationResult, error) {
		gotText, gotSource, gotTargets = text, sourceLang, targetLangs
		return &plugnmeet.InsightsTextTranslationResult{
			SourceText:   text,
			SourceLang:   sourceLang,
			Translations: map[string]string{"es-419": "buenos días", "fr-FR": "bonjour"},
		}, nil
	}

	opts := &insights.TranscriptionOptions{
		UserName:   "user-1",
		SpokenLang: "en",
		TransLangs: []string{"es-419", "fr-FR"},
	}
	stream := newTranscriptionStream(context.Background(), conn, opts, provider, logrus.NewEntry(logrus.New()))
	defer func() { _ = stream.Close() }()

	readEvent(t, stream.Results()) // session started

	final := readEvent(t, stream.Results())
	if final.Type != insights.EventTypeFinalResult || final.Result == nil {
		t.Fatalf("event = %+v, want a final result", final)
	}
	if gotText != "good morning" || gotSource != "en" {
		t.Errorf("translated %q from %q, want good morning from en", gotText, gotSource)
	}
	if len(gotTargets) != 2 || gotTargets[0] != "es-419" || gotTargets[1] != "fr-FR" {
		t.Errorf("target languages = %v", gotTargets)
	}
	if len(final.Result.Translations) != 2 || final.Result.Translations["fr-FR"] != "bonjour" {
		t.Fatalf("translations = %v, want the fan-out result", final.Result.Translations)
	}
}

func TestFinalTranscriptSurvivesTranslationFailure(t *testing.T) {
	conn := &fakeConn{
		block: make(chan struct{}),
		incoming: []*genai.LiveServerMessage{
			{ServerContent: &genai.LiveServerContent{
				InputTranscription: &genai.Transcription{Text: "good morning"},
			}},
		},
	}

	provider := newTestProvider()
	provider.translate = func(context.Context, string, string, []string) (*plugnmeet.InsightsTextTranslationResult, error) {
		return nil, errors.New("translation is down")
	}

	opts := &insights.TranscriptionOptions{UserName: "user-1", SpokenLang: "en", TransLangs: []string{"es-419"}}
	stream := newTranscriptionStream(context.Background(), conn, opts, provider, logrus.NewEntry(logrus.New()))
	defer func() { _ = stream.Close() }()

	readEvent(t, stream.Results()) // session started

	final := readEvent(t, stream.Results())
	if final.Type != insights.EventTypeFinalResult || final.Result == nil {
		t.Fatalf("event = %+v, want the final transcript anyway", final)
	}
	if final.Result.Text != "good morning" {
		t.Errorf("text = %q, want good morning", final.Result.Text)
	}
}

func TestTranscriptionGoAwayStopsTheSession(t *testing.T) {
	conn := &fakeConn{incoming: []*genai.LiveServerMessage{
		{GoAway: &genai.LiveServerGoAway{TimeLeft: time.Minute}},
	}}

	stream := newTranscriptionStream(context.Background(), conn,
		&insights.TranscriptionOptions{}, newTestProvider(), logrus.NewEntry(logrus.New()))

	readEvent(t, stream.Results()) // session started

	stopped := readEvent(t, stream.Results())
	if stopped.Type != insights.EventTypeSessionStopped {
		t.Fatalf("event = %q, want session stopped", stopped.Type)
	}
	if _, ok := <-stream.Results(); ok {
		t.Fatal("results channel must be closed after the session stops")
	}
	if !conn.closed {
		t.Error("socket must be closed after go away")
	}
}

func TestTranscriptionReceiveErrorReportsAndStops(t *testing.T) {
	conn := &fakeConn{}

	stream := newTranscriptionStream(context.Background(), conn,
		&insights.TranscriptionOptions{}, newTestProvider(), logrus.NewEntry(logrus.New()))

	readEvent(t, stream.Results()) // session started

	failure := readEvent(t, stream.Results())
	if failure.Type != insights.EventTypeError || failure.Error == "" {
		t.Fatalf("event = %+v, want an error", failure)
	}

	stopped := readEvent(t, stream.Results())
	if stopped.Type != insights.EventTypeSessionStopped {
		t.Fatalf("event = %q, want session stopped", stopped.Type)
	}
}

func TestTranscriptionCloseIsIdempotent(t *testing.T) {
	conn := &fakeConn{block: make(chan struct{})}
	stream := newTranscriptionStream(context.Background(), conn,
		&insights.TranscriptionOptions{}, newTestProvider(), logrus.NewEntry(logrus.New()))

	readEvent(t, stream.Results()) // session started

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	if !conn.closed {
		t.Fatal("socket was not closed")
	}
	if err := stream.WriteSample([]int16{1, 2}); err == nil {
		t.Fatal("WriteSample() after Close() must fail")
	}
	if _, ok := <-stream.Results(); ok {
		t.Fatal("results channel must be closed")
	}
}
