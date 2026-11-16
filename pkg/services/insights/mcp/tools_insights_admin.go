package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolConfigureSpeechToText         = "configure_speech_to_text"
	ToolConfigureChatTranslation      = "configure_chat_translation"
	ToolConfigureMeetingSummarization = "configure_meeting_summarization"

	// Starting one of these services waits for the insights agent to confirm, so
	// it can take longer than a plain room update.
	insightsConfigToolTimeout = 20 * time.Second
)

// speechToTextUserActionInstruction is returned after a successful start_or_update
// so the model tells the allowed users how to start their own speech session.
const speechToTextUserActionInstruction = "Speech-to-text is configured but does not start automatically " +
	"(spoken language is not auto-detected). Tell the allowed user(s): Please click the \"T\" icon in the " +
	"bottom menu bar, select your spoken language, and click Start to begin transcribing."

// allowedUsersRequiredMsg is returned when the model starts speech-to-text
// without saying whose speech should be transcribed.
const allowedUsersRequiredMsg = "allowed_users is required for start_or_update: pass [\"everyone\"] to allow " +
	"all participants, or pass specific participant names or user IDs (ask the user whom to activate if not specified)."

func registerInsightsTools(reg *Registry, deps ToolDependencies) {
	if deps.Insights == nil {
		return
	}

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: insightsConfigToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolConfigureSpeechToText,
			Description: "Start, update or stop live speech-to-text (transcription) for the room. Configuring " +
				"speech-to-text requires knowing which users are allowed to speak: specific participant names or " +
				"user IDs, or [\"everyone\"] to allow all participants. If the caller did not specify who should " +
				"have speech-to-text activated, ask them whether to enable it for everyone or only for specific " +
				"participants (or call get_room_info to check the participant names/IDs). Configuring " +
				"speech-to-text only enables the service in the room; it does NOT start transcribing automatically " +
				"because the spoken language is not auto-detected. After configuring, you MUST instruct the user(s) " +
				"to click the \"T\" icon in the bottom menu bar, select their spoken language, and click Start to " +
				"begin transcribing. Optionally translate the subtitles into other languages and read the " +
				"translation back as speech. Use start_or_update again to change the languages; use stop to end " +
				"transcription for everyone.",
			InputSchema: objectSchema(map[string]any{
				"action":       enumProp("Whether to start/update transcription or stop it.", "start_or_update", "stop"),
				"spoken_langs": arrayProp("BCP-47 languages the participants may speak, e.g. [\"en-US\"]. Required for start_or_update.", stringProp("", 20), 1, 5),
				"trans_langs":  arrayProp("Languages the subtitles are translated into.", stringProp("", 20), 0, 10),
				"allowed_users": arrayProp("Users whose speech is transcribed: participant names, user IDs, or "+
					"[\"everyone\"] (\"all\" also works) for every participant. Required for start_or_update; ask the "+
					"user whom to activate if they did not say.", stringProp("", 120), 0, 100),
				"allowed_user_ids": arrayProp("Alias of allowed_users: participant names, user IDs, or [\"everyone\"]. "+
					"Pass one of the two.", stringProp("", 120), 0, 100),
				"enable_synthesis": boolProp("Read the translated subtitles back as speech. Requires trans_langs."),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action         string   `json:"action"`
				SpokenLangs    []string `json:"spoken_langs"`
				TransLangs     []string `json:"trans_langs"`
				AllowedUsers   []string `json:"allowed_users"`
				AllowedUserIDs []string `json:"allowed_user_ids"`
				EnableSynth    bool     `json:"enable_synthesis"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			// allowed_user_ids is kept as an alias so either name works.
			allowedUsers := cleanLangs(append(append([]string{}, in.AllowedUsers...), in.AllowedUserIDs...))

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "stop":
			case "start_or_update":
				if len(in.SpokenLangs) == 0 {
					return errorResult(call, "spoken_langs is required for start_or_update, e.g. [\"en-US\"]"), nil
				}
				if len(allowedUsers) == 0 {
					return errorResult(call, "%s", allowedUsersRequiredMsg), nil
				}
				if in.EnableSynth && len(in.TransLangs) == 0 {
					return errorResult(call, "enable_synthesis requires at least one language in trans_langs"), nil
				}
			default:
				return errorResult(call, "action must be one of: start_or_update, stop"), nil
			}

			err := deps.Insights.ConfigureSpeechToText(ctx, call.RoomID, action,
				cleanLangs(in.SpokenLangs), cleanLangs(in.TransLangs), allowedUsers, in.EnableSynth)
			if err != nil {
				return errorResult(call, "speech-to-text could not be %s: %s", actionVerb(action), err.Error()), nil
			}

			out := map[string]any{
				"ok":      true,
				"service": "speech_to_text",
				"action":  action,
				"enabled": action == "start_or_update",
			}
			if action == "start_or_update" {
				out["allowed_users"] = allowedUsers
				out["user_action_required"] = speechToTextUserActionInstruction
			}

			return resultJSON(call, out), nil
		},
	})

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: insightsConfigToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolConfigureChatTranslation,
			Description: "Start, update or stop automatic translation of the room's chat messages. Participants pick " +
				"from the languages you allow; the default language is selected for them. Use stop to switch " +
				"translation off for the room.",
			InputSchema: objectSchema(map[string]any{
				"action":       enumProp("Whether to start/update chat translation or stop it.", "start_or_update", "stop"),
				"trans_langs":  arrayProp("Languages the chat messages are translated into. Required for start_or_update.", stringProp("", 20), 1, 10),
				"default_lang": stringProp("Language the participants see first, e.g. \"es\".", 20),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action      string   `json:"action"`
				TransLangs  []string `json:"trans_langs"`
				DefaultLang string   `json:"default_lang"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "stop":
			case "start_or_update":
				if len(in.TransLangs) == 0 {
					return errorResult(call, "trans_langs is required for start_or_update, e.g. [\"es\", \"fr\"]"), nil
				}
			default:
				return errorResult(call, "action must be one of: start_or_update, stop"), nil
			}

			err := deps.Insights.ConfigureChatTranslation(ctx, call.RoomID, action,
				cleanLangs(in.TransLangs), strings.TrimSpace(in.DefaultLang))
			if err != nil {
				return errorResult(call, "chat translation could not be %s: %s", actionVerb(action), err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":      true,
				"service": "chat_translation",
				"action":  action,
				"enabled": action == "start_or_update",
			}), nil
		},
	})

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: insightsConfigToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolConfigureMeetingSummarization,
			Description: "Start, update or stop the automatic summary of the meeting. The summary is generated for the " +
				"whole room, so tell the participants when you switch it on. Use stop to end summarization.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("Whether to start/update the meeting summary or stop it.", "start_or_update", "stop"),
				"summarization_prompt": stringProp("Optional instructions that shape the summary, e.g. what to focus on "+
					"or how long it should be.", 2000),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action              string `json:"action"`
				SummarizationPrompt string `json:"summarization_prompt"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "start_or_update", "stop":
			default:
				return errorResult(call, "action must be one of: start_or_update, stop"), nil
			}

			err := deps.Insights.ConfigureMeetingSummarization(ctx, call.RoomID, action, strings.TrimSpace(in.SummarizationPrompt))
			if err != nil {
				return errorResult(call, "meeting summarization could not be %s: %s", actionVerb(action), err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":      true,
				"service": "meeting_summarization",
				"action":  action,
				"enabled": action == "start_or_update",
			}), nil
		},
	})
}

// actionVerb turns start_or_update / stop into the wording of an error message.
func actionVerb(action string) string {
	if action == "stop" {
		return "stopped"
	}
	return "started"
}

// cleanLangs trims the language and user-id lists the model produced and drops
// the empty entries.
func cleanLangs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
