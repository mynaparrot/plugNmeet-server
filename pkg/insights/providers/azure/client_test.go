package azure

import (
	"context"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// Unsupported capabilities must fail loudly instead of returning nil, nil,
// which would leave callers with a nil channel or a silently empty result.
func TestAzureProviderUnsupportedMethodsReturnErrors(t *testing.T) {
	p := &AzureProvider{}
	ctx := context.Background()

	if stream, err := p.AITextChatStream(ctx, "model", nil); err == nil {
		t.Errorf("AITextChatStream: expected error, got stream=%v err=nil", stream)
	} else if !strings.Contains(err.Error(), "not supported for the azure provider") {
		t.Errorf("AITextChatStream: unexpected error %q", err)
	}

	if summary, promptTokens, completionTokens, err := p.AIChatTextSummarize(ctx, "model", nil); err == nil {
		t.Errorf("AIChatTextSummarize: expected error, got %q/%d/%d", summary, promptTokens, completionTokens)
	} else if !strings.Contains(err.Error(), "not supported for the azure provider") {
		t.Errorf("AIChatTextSummarize: unexpected error %q", err)
	}

	if jobId, fileName, err := p.StartBatchSummarizeAudioFile(ctx, "/tmp/audio.wav", "model", "prompt"); err == nil {
		t.Errorf("StartBatchSummarizeAudioFile: expected error, got %q/%q", jobId, fileName)
	} else if !strings.Contains(err.Error(), "not supported for the azure provider") {
		t.Errorf("StartBatchSummarizeAudioFile: unexpected error %q", err)
	}

	if res, err := p.CheckBatchJobStatus(ctx, "job"); err == nil {
		t.Errorf("CheckBatchJobStatus: expected error, got %v", res)
	} else if !strings.Contains(err.Error(), "not supported for the azure provider") {
		t.Errorf("CheckBatchJobStatus: unexpected error %q", err)
	}

	if err := p.DeleteUploadedFile(ctx, "file"); err == nil {
		t.Error("DeleteUploadedFile: expected error, got nil")
	} else if !strings.Contains(err.Error(), "not supported for the azure provider") {
		t.Errorf("DeleteUploadedFile: unexpected error %q", err)
	}
}

// GetSupportedLanguages must hand out freshly allocated entries, never pointers
// into the shared package-level slice.
func TestAzureProviderGetSupportedLanguagesReturnsFreshCopies(t *testing.T) {
	p := &AzureProvider{}
	serviceType := insights.ServiceTypeTranscription

	original := supportedLanguages[serviceType]
	if len(original) == 0 {
		t.Fatal("expected azure to ship transcription languages")
	}

	first := p.GetSupportedLanguages(serviceType)
	if len(first) != len(original) {
		t.Fatalf("expected %d languages, got %d", len(original), len(first))
	}
	if first[0].Code != original[0].Code || first[0].Name != original[0].Name || first[0].Locale != original[0].Locale {
		t.Errorf("first entry mismatch: got code=%q name=%q locale=%q", first[0].Code, first[0].Name, first[0].Locale)
	}

	// Mutating a returned entry must not leak into the shared list.
	first[0].Code = "mutated"
	first[0].Name = "mutated"
	second := p.GetSupportedLanguages(serviceType)
	if second[0].Code != original[0].Code {
		t.Errorf("shared list was mutated: code is now %q, want %q", second[0].Code, original[0].Code)
	}
	if second[0] == first[0] {
		t.Error("expected a fresh allocation per call, got the same pointer")
	}

	if got := p.GetSupportedLanguages(insights.ServiceTypeAITextChat); len(got) != 0 {
		t.Errorf("expected empty slice for unknown service type, got %d entries", len(got))
	}
}
