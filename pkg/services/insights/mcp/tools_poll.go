package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolCreatePoll     = "create_poll"
	ToolCreateQuiz     = "create_quiz"
	ToolClosePoll      = "close_poll"
	ToolDraftQuiz      = "draft_quiz"
	ToolPublishQuiz    = "publish_quiz"
	ToolGetPollResults = "get_poll_results"
)

// Quiz draft shape limits, mirrored by the draft_quiz input schema.
const (
	minQuizDraftOptions   = 2
	maxQuizDraftOptions   = 6
	maxQuizDraftIndex     = 5
	maxQuizDraftQuestions = 10
)

// quizDraftLatestKey is the room-scoped key of the accumulated pending draft.
// draft_quiz mirrors every draft under it, so publish_quiz can publish all
// questions even when the model lost the individual draft ids between turns.
const quizDraftLatestKey = "latest"

// quizDraftMergeWindow bounds how long draft_quiz appends to a pending room
// draft: a draft older than the Redis draft TTL is a new batch, not a retry.
const quizDraftMergeWindow = 30 * time.Minute

const quizDraftNextStep = "Display all drafted quiz questions and options in chat and wait for explicit user approval before calling publish_quiz."

var (
	errQuizDraftOptionRequired = errors.New("each quiz option must not be empty")
	errQuizDraftOptionCount    = fmt.Errorf("a quiz needs between %d and %d options", minQuizDraftOptions, maxQuizDraftOptions)
)

func registerPollTools(reg *Registry, deps ToolDependencies) {
	if deps.Poll == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolCreatePoll,
			Description: "Open a poll on the participant's screen to check understanding or gather responses. Supports " +
				"choice polls (provide 2-10 options) and free-text answer polls (set is_text_answer=true and omit " +
				"options so participants type their own text answer).",
			InputSchema: objectSchema(map[string]any{
				"question":       stringProp("The question.", 500),
				"options":        arrayProp("The answer options for a choice poll (2-10 options). Omit when is_text_answer is true.", stringProp("", 200), 0, 10),
				"is_text_answer": boolProp("True for a free-text poll where participants type a text answer instead of picking an option."),
			}, "question"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Question     string   `json:"question"`
				Options      []string `json:"options"`
				IsTextAnswer bool     `json:"is_text_answer"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			if in.IsTextAnswer {
				in.Options = nil
			} else if len(in.Options) < 2 {
				return errorResult(call, "a choice poll requires at least 2 options, or set is_text_answer=true for a free-text poll"), nil
			}
			pollID, err := deps.Poll.CreatePoll(ctx, call.RoomID, in.Question, in.Options, in.IsTextAnswer)
			if err != nil {
				return errorResult(call, "the poll could not be created: %s", err.Error()), nil
			}
			return resultJSON(call, map[string]any{
				"poll_id":        pollID,
				"is_quiz":        false,
				"is_text_answer": in.IsTextAnswer,
				"opened":         true,
			}), nil
		},
	})

	// A text chat session must preview first: it gets draft_quiz/publish_quiz
	// instead of the direct create_quiz the voice agent uses.
	if deps.RequireQuizApproval {
		registerQuizApprovalTools(reg, deps)
	} else {
		reg.Register(&Tool{
			Kind: ToolKindServer,
			Spec: &insights.MCPToolSpec{
				Name: ToolCreateQuiz,
				Description: "Open ONE multiple-choice quiz question on the participant's screen (never create multiple " +
					"quizzes at once; go progressively one question at a time and wait for the participant's answer). " +
					"The correct option is stored server-side for your feedback; never reveal it in the question.",
				InputSchema: objectSchema(map[string]any{
					"question":             stringProp("The question.", 500),
					"options":              arrayProp("The answer options.", stringProp("", 200), 2, 10),
					"correct_option_index": intProp("0-based index of the correct option.", 0, 9),
					"explanation":          stringProp("Short explanation to give after the answer.", 500),
				}, "question", "options", "correct_option_index"),
			},
			Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
				var in struct {
					Question           string   `json:"question"`
					Options            []string `json:"options"`
					CorrectOptionIndex int      `json:"correct_option_index"`
					Explanation        string   `json:"explanation"`
				}
				if err := decodeArgs(call, &in); err != nil {
					return errorResult(call, "%s", err.Error()), nil
				}
				if in.CorrectOptionIndex < 0 || in.CorrectOptionIndex >= len(in.Options) {
					return errorResult(call, "correct_option_index must be between 0 and %d", len(in.Options)-1), nil
				}
				pollID, err := deps.Poll.CreateQuiz(ctx, call.RoomID, in.Question, in.Options, in.CorrectOptionIndex)
				if err != nil {
					return errorResult(call, "the quiz could not be created: %s", err.Error()), nil
				}
				if deps.Quiz != nil {
					if err := deps.Quiz.SaveAIQuizMetadata(ctx, call.RoomID, pollID, in.CorrectOptionIndex, in.Explanation); err != nil {
						return errorResult(call, "the quiz was opened as poll %s but the answer key could not be stored: %s", pollID, err.Error()), nil
					}
				}
				return resultJSON(call, map[string]any{
					"poll_id": pollID,
					"is_quiz": true,
					"opened":  true,
				}), nil
			},
		})
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name:        ToolClosePoll,
			Description: "Close an open poll: results are revealed and no further answers are accepted.",
			InputSchema: objectSchema(map[string]any{
				"poll_id": stringProp("The poll id from create_poll/create_quiz.", 120),
			}, "poll_id"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				PollID string `json:"poll_id"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			if err := deps.Poll.ClosePoll(ctx, call.RoomID, in.PollID); err != nil {
				return errorResult(call, "the poll could not be closed: %s", err.Error()), nil
			}
			return resultJSON(call, map[string]any{"poll_id": in.PollID, "closed": true}), nil
		},
	})

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolGetPollResults,
			Description: "Inspect the room's polls and quizzes: omit poll_id to list every poll with status, vote " +
				"counts, and free-text answers, or pass one poll_id for its detailed results. Use it before feedback " +
				"or closing a poll.",
			InputSchema: objectSchema(map[string]any{
				"poll_id": stringProp("Optional poll id; omit it to list all polls.", 120),
			}),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				PollID string `json:"poll_id"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			pollID := strings.TrimSpace(in.PollID)
			results, err := deps.Poll.GetPollResults(ctx, call.RoomID, pollID)
			if err != nil {
				return errorResult(call, "the poll results could not be read: %s", err.Error()), nil
			}
			if pollID == "" {
				return resultJSON(call, map[string]any{"polls": results}), nil
			}
			return resultJSON(call, map[string]any{"poll": results}), nil
		},
	})
}

// registerQuizApprovalTools adds the two-step quiz flow: draft_quiz shows the
// preview in chat, publish_quiz only runs after the host approved it.
func registerQuizApprovalTools(reg *Registry, deps ToolDependencies) {
	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolDraftQuiz,
			Description: "Create a quiz draft to preview in chat for admin approval before publishing. Call it once " +
				"with `questions` for a multi-question quiz, or once per question with the top-level fields; " +
				"single-question drafts accumulate in the room's pending draft. Base the questions on the user's " +
				"request, or on external MCP lesson/resource tools or notepad content when relevant. Then display " +
				"every drafted question (numbered options, " +
				"correct answer, explanation) in chat and ask for explicit approval before publish_quiz.",
			InputSchema: objectSchema(map[string]any{
				"questions": arrayProp("1-10 questions for a multi-question quiz; preferred over the top-level fields.",
					quizDraftQuestionSchema(), 1, maxQuizDraftQuestions),
				"question": stringProp("Single-question form: the question shown once published.", 500),
				"options":  arrayProp("Single-question form: the answer options in display order.", stringProp("", 200), minQuizDraftOptions, maxQuizDraftOptions),
				"correct_option_index": intProp("Single-question form: 0-based index of the correct option.",
					0, maxQuizDraftIndex),
				"explanation": stringProp("Single-question form: short explanation to give after the answer.", 500),
			}),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in quizDraftInput
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			questions, err := quizDraftQuestionsFromInput(in)
			if err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			store := deps.quizDrafts()
			now := time.Now().Unix()
			draftID := newQuizDraftID()

			// A single-question call continues the room's pending draft, so a
			// model that drafts question by question keeps all of them.
			pending, found, err := store.GetAIQuizDraft(ctx, call.RoomID, quizDraftLatestKey)
			if err != nil {
				return errorResult(call, "the quiz draft could not be read: %s", err.Error()), nil
			}
			if found && pending != nil && now-pending.CreatedAt <= int64(quizDraftMergeWindow/time.Second) {
				if len(questions) == 1 {
					merged := append([]insights.AIQuizDraftQuestion{}, pending.AllQuestions()...)
					if !quizDraftHasQuestion(merged, questions[0]) {
						if len(merged) >= maxQuizDraftQuestions {
							return errorResult(call, "a pending quiz draft can hold at most %d questions; publish it before drafting more", maxQuizDraftQuestions), nil
						}
						merged = append(merged, questions[0])
					}
					questions = merged
					if pending.DraftID != "" {
						draftID = pending.DraftID
					}
				} else if pending.DraftID != "" && pending.DraftID != draftID {
					// A multi-question call is a fresh batch: it replaces the
					// pending draft instead of growing it forever.
					if err := store.DeleteAIQuizDraft(ctx, call.RoomID, pending.DraftID); err != nil {
						return errorResult(call, "the previous quiz draft could not be replaced: %s", err.Error()), nil
					}
				}
			}

			draft := &QuizDraft{
				DraftID:            draftID,
				Question:           questions[0].Question,
				Options:            questions[0].Options,
				CorrectOptionIndex: questions[0].CorrectOptionIndex,
				Explanation:        questions[0].Explanation,
				Questions:          questions,
				CreatedAt:          now,
			}
			// The "latest" key mirrors the draft so publish_quiz can find all
			// questions even when the model lost the draft ids between turns.
			for _, key := range []string{quizDraftLatestKey, draft.DraftID} {
				if err := store.SaveAIQuizDraft(ctx, call.RoomID, key, draft); err != nil {
					return errorResult(call, "the quiz draft could not be stored: %s", err.Error()), nil
				}
			}

			previews := make([]map[string]any, 0, len(draft.Questions))
			for _, question := range draft.Questions {
				previews = append(previews, quizDraftQuestionPreview(question))
			}

			return resultJSON(call, map[string]any{
				"draft_id":       draft.DraftID,
				"status":         "pending_approval",
				"question_count": len(draft.Questions),
				"questions":      previews,
				"preview":        previews[0],
				"next_step":      quizDraftNextStep,
			}), nil
		},
	})

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolPublishQuiz,
			Description: "Publish the room's pending quiz draft after the user approved the preview in chat. Omit " +
				"draft_id (or pass \"latest\"/\"all\") to publish all pending questions.",
			InputSchema: objectSchema(map[string]any{
				"draft_id": stringProp("Optional draft_id from draft_quiz; omit or pass \"latest\"/\"all\" for all pending questions.", 120),
			}),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				DraftID string `json:"draft_id"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			store := deps.quizDrafts()
			draftID := strings.TrimSpace(in.DraftID)

			latest, latestFound, err := store.GetAIQuizDraft(ctx, call.RoomID, quizDraftLatestKey)
			if err != nil {
				return errorResult(call, "the quiz draft could not be read: %s", err.Error()), nil
			}

			var draft *QuizDraft
			switch draftID {
			case "", "all", "latest":
				if latestFound {
					draft = latest
				}
			default:
				exact, found, err := store.GetAIQuizDraft(ctx, call.RoomID, draftID)
				if err != nil {
					return errorResult(call, "the quiz draft could not be read: %s", err.Error()), nil
				}
				if found {
					draft = exact
				}
				// The tool output of the drafting turn is not part of the text
				// history, so the model may invent a draft id. A pending room
				// draft still wins when it holds this batch's questions; when
				// the room has no pending draft at all, the error below is
				// returned as before.
				if latestFound && latest != nil {
					if draft == nil {
						draft = latest
					} else if quizDraftContainsAll(latest, draft) {
						draft = latest
					}
				}
			}

			questions := draft.AllQuestions()
			if draft == nil || len(questions) == 0 {
				return errorResult(call, "no pending quiz draft %q; call draft_quiz and get the user's approval first", draftID), nil
			}
			if len(questions) > maxQuizDraftQuestions {
				return errorResult(call, "the quiz draft holds more than %d questions; draft a smaller quiz", maxQuizDraftQuestions), nil
			}

			// The draft is consumed before publishing, so a retry can never
			// publish the same approved quiz twice.
			consumed := []string{draft.DraftID}
			if draft == latest || (latestFound && latest != nil && latest.DraftID == draft.DraftID) {
				consumed = append(consumed, quizDraftLatestKey)
			}
			if draftID != "" && draftID != "all" && draftID != "latest" {
				consumed = append(consumed, draftID)
			}
			for _, key := range dedupeStrings(consumed) {
				if err := store.DeleteAIQuizDraft(ctx, call.RoomID, key); err != nil {
					return errorResult(call, "the quiz draft could not be consumed: %s", err.Error()), nil
				}
			}

			pollIDs := make([]string, 0, len(questions))
			for i, question := range questions {
				pollID, err := deps.Poll.CreateQuiz(ctx, call.RoomID, question.Question, question.Options, question.CorrectOptionIndex)
				if err != nil {
					return errorResult(call, "quiz %d of %d could not be published: %s", i+1, len(questions), err.Error()), nil
				}
				if deps.Quiz != nil {
					if err := deps.Quiz.SaveAIQuizMetadata(ctx, call.RoomID, pollID, question.CorrectOptionIndex, question.Explanation); err != nil {
						return errorResult(call, "the quiz was opened as poll %s but the answer key could not be stored: %s", pollID, err.Error()), nil
					}
				}
				pollIDs = append(pollIDs, pollID)
			}

			return resultJSON(call, map[string]any{
				"poll_id":         pollIDs[0],
				"poll_ids":        pollIDs,
				"published_count": len(pollIDs),
				"draft_id":        draft.DraftID,
				"is_quiz":         true,
				"opened":          true,
			}), nil
		},
	})
}

// cleanQuizDraftOptions trims the preview's options and enforces the draft
// shape the model was told to use.
func cleanQuizDraftOptions(options []string) ([]string, error) {
	cleaned := make([]string, 0, len(options))
	for _, option := range options {
		option = truncateRunes(strings.TrimSpace(option), 200)
		if option == "" {
			return nil, errQuizDraftOptionRequired
		}
		cleaned = append(cleaned, option)
	}
	if len(cleaned) < minQuizDraftOptions || len(cleaned) > maxQuizDraftOptions {
		return nil, errQuizDraftOptionCount
	}
	return cleaned, nil
}

// quizDraftInput accepts either a questions array or the single-question
// top-level fields, so one draft_quiz call can describe a whole quiz.
type quizDraftInput struct {
	Questions          []quizDraftQuestionInput `json:"questions"`
	Question           string                   `json:"question"`
	Options            []string                 `json:"options"`
	CorrectOptionIndex int                      `json:"correct_option_index"`
	Explanation        string                   `json:"explanation"`
}

type quizDraftQuestionInput struct {
	Question           string   `json:"question"`
	Options            []string `json:"options"`
	CorrectOptionIndex int      `json:"correct_option_index"`
	Explanation        string   `json:"explanation"`
}

func quizDraftQuestionSchema() map[string]any {
	return objectSchema(map[string]any{
		"question":             stringProp("The question shown to the participants once the quiz is published.", 500),
		"options":              arrayProp("The answer options, in display order.", stringProp("", 200), minQuizDraftOptions, maxQuizDraftOptions),
		"correct_option_index": intProp("0-based index of the correct option.", 0, maxQuizDraftIndex),
		"explanation":          stringProp("Optional short explanation to give after the participants answer.", 500),
	}, "question", "options", "correct_option_index")
}

// quizDraftQuestionsFromInput turns both accepted input shapes into validated
// draft questions.
func quizDraftQuestionsFromInput(in quizDraftInput) ([]insights.AIQuizDraftQuestion, error) {
	raw := in.Questions
	if len(raw) == 0 {
		raw = []quizDraftQuestionInput{{
			Question:           in.Question,
			Options:            in.Options,
			CorrectOptionIndex: in.CorrectOptionIndex,
			Explanation:        in.Explanation,
		}}
	}
	if len(raw) > maxQuizDraftQuestions {
		return nil, fmt.Errorf("questions must not contain more than %d items", maxQuizDraftQuestions)
	}

	questions := make([]insights.AIQuizDraftQuestion, 0, len(raw))
	for _, item := range raw {
		question := truncateRunes(strings.TrimSpace(item.Question), 500)
		if question == "" {
			return nil, errors.New("question must not be empty")
		}
		options, err := cleanQuizDraftOptions(item.Options)
		if err != nil {
			return nil, err
		}
		if item.CorrectOptionIndex < 0 || item.CorrectOptionIndex >= len(options) {
			return nil, fmt.Errorf("correct_option_index must be between 0 and %d", len(options)-1)
		}
		questions = append(questions, insights.AIQuizDraftQuestion{
			Question:           question,
			Options:            options,
			CorrectOptionIndex: item.CorrectOptionIndex,
			Explanation:        truncateRunes(strings.TrimSpace(item.Explanation), 500),
		})
	}
	return questions, nil
}

func quizDraftQuestionPreview(question insights.AIQuizDraftQuestion) map[string]any {
	preview := map[string]any{
		"question":             question.Question,
		"options":              question.Options,
		"correct_option_index": question.CorrectOptionIndex,
	}
	if question.CorrectOptionIndex >= 0 && question.CorrectOptionIndex < len(question.Options) {
		preview["correct_answer"] = question.Options[question.CorrectOptionIndex]
	}
	if question.Explanation != "" {
		preview["explanation"] = question.Explanation
	}
	return preview
}

func sameQuizDraftQuestion(a, b insights.AIQuizDraftQuestion) bool {
	if !strings.EqualFold(strings.TrimSpace(a.Question), strings.TrimSpace(b.Question)) ||
		a.CorrectOptionIndex != b.CorrectOptionIndex || len(a.Options) != len(b.Options) {
		return false
	}
	for i := range a.Options {
		if a.Options[i] != b.Options[i] {
			return false
		}
	}
	return true
}

func quizDraftHasQuestion(questions []insights.AIQuizDraftQuestion, want insights.AIQuizDraftQuestion) bool {
	for _, question := range questions {
		if sameQuizDraftQuestion(question, want) {
			return true
		}
	}
	return false
}

// quizDraftContainsAll reports whether container holds every question of
// subset, so an accumulated room draft can publish a whole batch.
func quizDraftContainsAll(container, subset *QuizDraft) bool {
	if container == nil || subset == nil {
		return false
	}
	have := container.AllQuestions()
	if len(have) == 0 {
		return false
	}
	for _, want := range subset.AllQuestions() {
		if !quizDraftHasQuestion(have, want) {
			return false
		}
	}
	return true
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
