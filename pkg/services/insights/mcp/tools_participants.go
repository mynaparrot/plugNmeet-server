package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolManageParticipants = "manage_participants"

	participantsToolTimeout = 15 * time.Second

	// targetAllUsers is the target that addresses every participant of the room.
	targetAllUsers = "all"
)

// participantLockServices are the lock services manage_participants exposes.
// They map onto the room's lock settings one to one.
var participantLockServices = []string{
	"mic", "webcam", "screenShare", "chat", "sendChatMsg", "whiteboard", "sharedNotepad",
}

func registerParticipantTools(reg *Registry, deps ToolDependencies) {
	if deps.Participants == nil {
		return
	}

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: participantsToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageParticipants,
			Description: "Moderate the room: mute or unmute microphones, hand the presenter role to a participant, " +
				"admit the people waiting in the waiting room, or lock and unlock a room surface (mic, webcam, " +
				"screen share, chat, whiteboard, shared notepad). Use target_user_id=\"all\" to address everyone " +
				"where that makes sense. Announce a mute or a lock before you apply it.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("What to do with the participants.",
					"mute_microphone", "unmute_microphone", "switch_presenter", "approve_waiting_users", "update_lock_settings"),
				"target_user_id": stringProp("The participant's user_id, or \"all\" for all participants.", 120),
				"lock_service": enumProp("The surface to lock or unlock; only for update_lock_settings.",
					participantLockServices...),
				"locked": boolProp("True locks the surface, false unlocks it; only for update_lock_settings. " +
					"Defaults to true when omitted."),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action       string `json:"action"`
				TargetUserID string `json:"target_user_id"`
				LockService  string `json:"lock_service"`
				Locked       *bool  `json:"locked"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			target := strings.TrimSpace(in.TargetUserID)
			lockService := strings.TrimSpace(in.LockService)

			switch action {
			case "mute_microphone", "unmute_microphone":
				if target == "" {
					target = targetAllUsers
				}
			case "switch_presenter", "approve_waiting_users":
				if target == "" || target == targetAllUsers {
					return errorResult(call, "target_user_id of one participant is required for %s", action), nil
				}
			case "update_lock_settings":
				if !containsString(participantLockServices, lockService) {
					return errorResult(call, "lock_service must be one of: %s", strings.Join(participantLockServices, ", ")), nil
				}
				if target == "" {
					target = targetAllUsers
				}
			default:
				return errorResult(call, "action must be one of: mute_microphone, unmute_microphone, switch_presenter, "+
					"approve_waiting_users, update_lock_settings"), nil
			}

			// Locking is the safe default: an explicit locked=false is needed to
			// open a surface.
			locked := true
			if in.Locked != nil {
				locked = *in.Locked
			}

			err := deps.Participants.ManageParticipants(ctx, call.RoomID, call.AgentUserID, action, target, lockService, locked)
			if err != nil {
				return errorResult(call, "the participants could not be updated: %s", err.Error()), nil
			}

			out := map[string]any{
				"ok":             true,
				"action":         action,
				"target_user_id": target,
			}
			if action == "update_lock_settings" {
				out["lock_service"] = lockService
				out["locked"] = locked
			}
			return resultJSON(call, out), nil
		},
	})
}
