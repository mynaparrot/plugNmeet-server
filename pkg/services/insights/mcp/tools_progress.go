package mcp

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolRecordLearningProgress = "record_learning_progress"

	// Concept mastery states.
	ProgressStatusIntroduced  = "introduced"
	ProgressStatusPracticing  = "practicing"
	ProgressStatusMastered    = "mastered"
	ProgressStatusNeedsReview = "needs_review"
)

func registerProgressTools(reg *Registry, deps ToolDependencies) {
	if deps.Progress == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolRecordLearningProgress,
			Description: "Record what the participant has learned for the next session summary. " +
				"Call it when a concept is introduced, practised, mastered or needs review.",
			InputSchema: objectSchema(map[string]any{
				"concept": stringProp("The concept, e.g. \"past tense of irregular verbs\".", 120),
				"status":  enumProp("How well the participant knows it now.", ProgressStatusIntroduced, ProgressStatusPracticing, ProgressStatusMastered, ProgressStatusNeedsReview),
				"notes":   stringProp("Optional short note about the observation.", 500),
			}, "concept", "status"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			entry := ProgressEntry{}
			if err := decodeArgs(call, &entry); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			if err := deps.Progress.RecordAILearningProgress(ctx, call.RoomID, entry); err != nil {
				return errorResult(call, "the progress could not be recorded: %s", err.Error()), nil
			}
			return resultJSON(call, map[string]any{
				"saved":   true,
				"concept": entry.Concept,
				"status":  entry.Status,
			}), nil
		},
	})
}
