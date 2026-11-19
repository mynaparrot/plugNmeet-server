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
			Description: "Run the countdown timer every participant sees. Use it to time an exercise or a break: " +
				"start with a duration, then pause, resume, extend or stop it. Say the time out loud so the " +
				"participants know what the timer is for.",
			InputSchema: objectSchema(map[string]any{
				"action":           enumProp("What to do with the timer.", "start", "pause", "resume", "extend", "stop"),
				"duration_seconds": intProp("How long the timer runs (required for start) or how many seconds to add for extend (optional, defaults to 60).", minCountdownTimerSeconds, maxCountdownTimerSeconds),
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

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: roomOpsToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageExternalMedia,
			Description: "Share media with the room: play an audio/video file in the media player, or open a web page " +
				"in the external link view. Use stop to close whichever surface is open.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("Whether to start or stop the media.", "start", "stop"),
				"media_type": enumProp("Which surface to use: the media player for a direct media file, the external "+
					"link view for a web page.", "media_player", "external_link"),
				"url": stringProp("The https URL to play or open; required for start.", 1000),
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
			Description: "Start or stop the recording of the meeting. Recording starts and stops for the whole room, so " +
				"tell the participants before you start it. It is only available when the host enabled recording.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("Whether to start or stop the recording.", "start", "stop"),
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
