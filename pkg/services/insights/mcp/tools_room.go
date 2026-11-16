package mcp

import (
	"context"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolGetRoomInfo      = "get_room_info"
	ToolSetWorkspaceLock = "set_workspace_lock"
)

// ParticipantSummary is the room-facing view of one participant.
type ParticipantSummary struct {
	UserID              string `json:"user_id"`
	Name                string `json:"name"`
	IsPresenter         bool   `json:"is_presenter"`
	IsAdmin             bool   `json:"is_admin"`
	WhiteboardLocked    bool   `json:"whiteboard_locked"`
	SharedNotepadLocked bool   `json:"shared_notepad_locked"`
}

// RoomSnapshot is the read-only room state the agent may look at.
type RoomSnapshot struct {
	RoomTitle            string               `json:"room_title"`
	Participants         []ParticipantSummary `json:"participants"`
	ActiveWhiteboardPage int                  `json:"active_whiteboard_page"`
	ActivePollID         string               `json:"active_poll_id,omitempty"`
}

// RoomInfoBackend reads and adjusts the room state.
func registerRoomTools(reg *Registry, deps ToolDependencies) {
	if deps.Room == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		// Read-only: stays callable while the session is scope-locked.
		Essential: true,
		Spec: &insights.MCPToolSpec{
			Name: ToolGetRoomInfo,
			Description: "Read the current room state: room title, participants, the whiteboard page the participant sees " +
				"and the poll that is currently open.",
			InputSchema: objectSchema(map[string]any{}),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			snapshot, err := deps.Room.GetRoomSnapshot(ctx, call.RoomID)
			if err != nil {
				return errorResult(call, "the room information is not available right now: %s", err.Error()), nil
			}
			if snapshot == nil {
				return errorResult(call, "the room information is not available right now."), nil
			}
			return resultJSON(call, snapshot), nil
		},
	})
}

// registerWorkspaceLockTool exposes an explicit whiteboard / shared-notepad
// unlock so the agent can open a surface and hand the pen to the participant.
func registerWorkspaceLockTool(reg *Registry, deps ToolDependencies) {
	if deps.Room == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolSetWorkspaceLock,
			Description: "Unlock or lock the whiteboard or shared notepad for participants so they can draw or " +
				"write (and automatically open/show that surface when unlocking). Call this with action=\"unlock\" " +
				"whenever you ask a participant to draw on the whiteboard or write in the shared notepad without " +
				"drawing something yourself first.",
			InputSchema: objectSchema(map[string]any{
				"surface": enumProp("Which workspace surface to unlock or lock.", "whiteboard", "shared_notepad", "both"),
				"action":  enumProp("Whether to unlock (allow participant to draw/write) or lock the surface.", "unlock", "lock"),
				"user_id": stringProp("Optional specific participant user_id, or omit/'all' for all participants in the room.", 120),
			}, "surface", "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Surface string `json:"surface"`
				Action  string `json:"action"`
				UserID  string `json:"user_id"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			surface := strings.ToLower(strings.TrimSpace(in.Surface))
			action := strings.ToLower(strings.TrimSpace(in.Action))
			locked := false
			switch action {
			case "unlock":
			case "lock":
				locked = true
			default:
				return errorResult(call, "action must be unlock or lock"), nil
			}

			userID := strings.TrimSpace(in.UserID)
			if userID == "" {
				userID = "all"
			}

			if err := deps.Room.SetWorkspaceLock(ctx, call.RoomID, surface, locked, userID); err != nil {
				return errorResult(call, "the workspace lock could not be changed: %s", err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":      true,
				"surface": surface,
				"action":  action,
				"locked":  locked,
				"user_id": userID,
			}), nil
		},
	})
}
