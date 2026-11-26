package models

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

// activityRulesFeatures returns a minimal valid agent feature set where every
// workspace capability is enabled and the rules are attached.
func activityRulesFeatures(rules *plugnmeet.InsightsAIAgentActivityRules) *plugnmeet.InsightsAIAgentFeatures {
	return &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:              true,
		SystemPrompt:         "teach fractions",
		Topic:                "fractions",
		AllowWhiteboardWrite: true,
		AllowPolls:           true,
		AllowNotepadWrite:    true,
		ActivityRules:        rules,
		// a BYOK provider keeps the validator independent of the server config
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider: aiAgentCustomProviderGoogle,
			ApiKey:   "google-key",
		},
	}
}

// An unusable knowledge prefix must survive normalization: dropping it would stop
// validateAiExternalUrls from refusing the room.
func TestNormalizeAIAgentFeaturesKeepsKnowledgeURLsForValidation(t *testing.T) {
	out := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
		AllowedKnowledgeUrls: []string{" https://docs.example.com/kb ", "https://docs.example.com/kb", "http://insecure.example.com/kb"},
	})

	want := []string{"https://docs.example.com/kb", "http://insecure.example.com/kb"}
	if got := out.GetAllowedKnowledgeUrls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("knowledge urls = %v, want %v", got, want)
	}
}

func TestNormalizeAIAgentFeaturesActivityRules(t *testing.T) {
	long := strings.Repeat("m", maxAIAgentMilestoneRunes+10)
	milestones := []string{" warm up ", "", "warm up", long}
	for i := 0; i <= maxAIAgentMilestoneItems; i++ {
		milestones = append(milestones, fmt.Sprintf("step %d", i))
	}

	in := activityRulesFeatures(&plugnmeet.InsightsAIAgentActivityRules{
		MinWhiteboardDrawings: 2,
		MinQuizzes:            3,
		RequiredMilestones:    milestones,
	})
	out := NormalizeAIAgentFeatures(in)

	got := out.GetActivityRules().GetRequiredMilestones()
	if len(got) != maxAIAgentMilestoneItems {
		t.Fatalf("milestones = %d, want the cap of %d", len(got), maxAIAgentMilestoneItems)
	}
	if got[0] != "warm up" {
		t.Errorf("milestones[0] = %q, want the trimmed, deduped entry", got[0])
	}
	if runes := []rune(got[1]); len(runes) != maxAIAgentMilestoneRunes {
		t.Errorf("milestones[1] has %d runes, want the cap of %d", len(runes), maxAIAgentMilestoneRunes)
	}
	if last := got[len(got)-1]; last != fmt.Sprintf("step %d", maxAIAgentMilestoneItems-3) {
		t.Errorf("last milestone = %q, want the host order to survive the cap", last)
	}
	if out.GetActivityRules().GetMinWhiteboardDrawings() != 2 || out.GetActivityRules().GetMinQuizzes() != 3 {
		t.Errorf("quotas = %+v, want the host values to survive normalization", out.GetActivityRules())
	}

	// the stored metadata is never mutated
	if len(in.GetActivityRules().GetRequiredMilestones()) != len(milestones) {
		t.Errorf("normalization mutated the input: %d milestones left", len(in.GetActivityRules().GetRequiredMilestones()))
	}

	// a room without rules keeps none
	if rules := NormalizeAIAgentFeatures(activityRulesFeatures(nil)).GetActivityRules(); rules != nil {
		t.Errorf("rules = %+v, want nil for a room without activity rules", rules)
	}
}

func TestValidateAIAgentActivityRules(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	m.appConfig = &config.AppConfig{Insights: &config.InsightsConfig{}}

	tests := []struct {
		name            string
		rules           *plugnmeet.InsightsAIAgentActivityRules
		whiteboardWrite bool
		polls           bool
		notepadWrite    bool
		wantErr         bool
	}{
		{
			name:            "every quota at its ceiling",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinWhiteboardDrawings: aiAgentMaxActivityQuota, MinQuizzes: aiAgentMaxActivityQuota, MinNotepadEntries: aiAgentMaxActivityQuota, MinParticipantExercises: aiAgentMaxActivityQuota, RequiredMilestones: make([]string, maxAIAgentMilestoneItems)},
			whiteboardWrite: true,
			polls:           true,
			notepadWrite:    true,
		},
		{
			name:            "no rules",
			whiteboardWrite: true,
		},
		{
			name:            "whiteboard quota above the ceiling",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinWhiteboardDrawings: aiAgentMaxActivityQuota + 1},
			whiteboardWrite: true,
			wantErr:         true,
		},
		{
			name:            "quiz quota above the ceiling",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinQuizzes: aiAgentMaxActivityQuota + 1},
			whiteboardWrite: true,
			polls:           true,
			wantErr:         true,
		},
		{
			name:            "notepad quota above the ceiling",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinNotepadEntries: aiAgentMaxActivityQuota + 1},
			whiteboardWrite: true,
			notepadWrite:    true,
			wantErr:         true,
		},
		{
			name:            "participant exercise quota above the ceiling",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinParticipantExercises: aiAgentMaxActivityQuota + 1},
			whiteboardWrite: true,
			wantErr:         true,
		},
		{
			name:            "too many milestones",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{RequiredMilestones: make([]string, maxAIAgentMilestoneItems+1)},
			whiteboardWrite: true,
			wantErr:         true,
		},
		{
			name:            "whiteboard quota without whiteboard write",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinWhiteboardDrawings: 1},
			whiteboardWrite: false,
			polls:           true,
			notepadWrite:    true,
			wantErr:         true,
		},
		{
			name:            "quiz quota without polls",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinQuizzes: 1},
			whiteboardWrite: true,
			polls:           false,
			notepadWrite:    true,
			wantErr:         true,
		},
		{
			name:            "notepad quota without notepad write",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinNotepadEntries: 1},
			whiteboardWrite: true,
			polls:           true,
			notepadWrite:    false,
			wantErr:         true,
		},
		{
			name:            "exercise quota without a writable surface",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinParticipantExercises: 1},
			whiteboardWrite: false,
			polls:           true,
			notepadWrite:    false,
			wantErr:         true,
		},
		{
			name:            "exercise quota with only the notepad",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{MinParticipantExercises: 1},
			whiteboardWrite: false,
			polls:           true,
			notepadWrite:    true,
		},
		{
			name:            "milestones need no capability",
			rules:           &plugnmeet.InsightsAIAgentActivityRules{RequiredMilestones: []string{"warm up", "draw a diagram"}},
			whiteboardWrite: false,
			polls:           false,
			notepadWrite:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			features := activityRulesFeatures(tt.rules)
			features.AllowWhiteboardWrite = tt.whiteboardWrite
			features.AllowPolls = tt.polls
			features.AllowNotepadWrite = tt.notepadWrite

			err := m.ValidateAIAgentFeatures(features, nil, true)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, config.ErrAIAgentInvalidRequest) {
				t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
			}
		})
	}
}
