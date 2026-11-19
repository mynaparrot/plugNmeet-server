package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolManageCountdownTimer = "manage_countdown_timer"
	ToolManageExternalMedia  = "manage_external_media"
	ToolManageRecording      = "manage_recording"

	// Timer, media and recording changes reach the participants (and, for the
	// recording, the recorder) before they are acknowledged.
	roomOpsToolTimeout = 20 * time.Second

	minCountdownTimerSeconds = 10
	maxCountdownTimerSeconds = 7200
)

func registerRoomOpsTools(reg *Registry, deps ToolDependencies) {
	if deps.RoomOps == nil {
		return
	}

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: roomOpsToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageCountdownTimer,
			Description: "Run the countdown timer every participant sees, e.g. to time an exercise or a break. " +
				"Say the time out loud so the participants know what it is for.",
			InputSchema: objectSchema(map[string]any{
				"action":           enumProp("Timer action.", "start", "pause", "resume", "extend", "stop"),
				"duration_seconds": intProp("Seconds to run (start) or to add (extend, optional, defaults to 60).", minCountdownTimerSeconds, maxCountdownTimerSeconds),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action          string `json:"action"`
				DurationSeconds int    `json:"duration_seconds"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "start":
				if in.DurationSeconds < minCountdownTimerSeconds || in.DurationSeconds > maxCountdownTimerSeconds {
					return errorResult(call, "duration_seconds must be between %d and %d",
						minCountdownTimerSeconds, maxCountdownTimerSeconds), nil
				}
			case "extend":
				if in.DurationSeconds == 0 {
					in.DurationSeconds = 60
				}
				if in.DurationSeconds < minCountdownTimerSeconds || in.DurationSeconds > maxCountdownTimerSeconds {
					return errorResult(call, "duration_seconds must be between %d and %d",
						minCountdownTimerSeconds, maxCountdownTimerSeconds), nil
				}
			case "pause", "resume", "stop":
			default:
				return errorResult(call, "action must be one of: start, pause, resume, extend, stop"), nil
			}

			err := deps.RoomOps.ManageCountdownTimer(ctx, call.RoomID, call.AgentUserID, action, uint64(in.DurationSeconds))
			if err != nil {
				return errorResult(call, "the countdown timer could not be updated: %s", err.Error()), nil
			}

			out := map[string]any{"ok": true, "action": action}
			if action == "start" || action == "extend" {
				out["duration_seconds"] = in.DurationSeconds
			}
			return resultJSON(call, out), nil
		},
	})

	// The 1-on-1 voice agent only needs the countdown timer; sharing media and
	// recording are administrator actions of the AI text chat.
	if deps.MCPScope == MCPScopeAIAgent {
		return
	}

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: roomOpsToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageExternalMedia,
			Description: "Share media with the room: play an audio/video file in the media player or open a web page " +
				"in the external link view; use stop to close it.",
			InputSchema: objectSchema(map[string]any{
				"action":     enumProp("Start or stop the media.", "start", "stop"),
				"media_type": enumProp("Media player for a direct media file, external link for a web page.", "media_player", "external_link"),
				"url":        stringProp("https URL to play or open; required for start.", 1000),
			}, "action", "media_type"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action    string `json:"action"`
				MediaType string `json:"media_type"`
				URL       string `json:"url"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			mediaType := strings.ToLower(strings.TrimSpace(in.MediaType))
			mediaURL := strings.TrimSpace(in.URL)
			switch action {
			case "start":
				if mediaURL == "" {
					return errorResult(call, "url is required to start external media"), nil
				}
			case "stop":
			default:
				return errorResult(call, "action must be one of: start, stop"), nil
			}
			switch mediaType {
			case "media_player", "external_link":
			default:
				return errorResult(call, "media_type must be one of: media_player, external_link"), nil
			}

			err := deps.RoomOps.ManageExternalMedia(ctx, call.RoomID, call.AgentUserID, action, mediaType, mediaURL)
			if err != nil {
				return errorResult(call, "the external media could not be changed: %s", err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":         true,
				"action":     action,
				"media_type": mediaType,
			}), nil
		},
	})

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: roomOpsToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageRecording,
			Description: "Start or stop the recording of the whole room. Only available when the host enabled " +
				"recording; tell the participants before you start it.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("Start or stop the recording.", "start", "stop"),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action string `json:"action"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			switch action {
			case "start", "stop":
			default:
				return errorResult(call, "action must be one of: start, stop"), nil
			}

			if err := deps.RoomOps.ManageRecording(ctx, call.RoomID, action); err != nil {
				return errorResult(call, "the recording could not be %s: %s", actionVerb(action), err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":        true,
				"action":    action,
				"recording": action == "start",
			}), nil
		},
	})
}
