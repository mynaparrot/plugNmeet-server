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

// registerWorkspaceLockTool exposes explicit whiteboard / shared-notepad control:
// open, close, unlock and lock, so the agent owns both surfaces for the session.
func registerWorkspaceLockTool(reg *Registry, deps ToolDependencies) {
	if deps.Room == nil {
		return
	}

	reg.Register(&Tool{
		Kind: ToolKindServer,
		Spec: &insights.MCPToolSpec{
			Name: ToolSetWorkspaceLock,
			Description: "Control the whiteboard or shared notepad for participants. Keep surfaces locked (view-only) while " +
				"you (the AI agent) are drawing, writing, or explaining — your own tools always work while locked, and " +
				"calling any drawing or notepad tool automatically opens the surface in view-only mode. Use 'unlock' ONLY " +
				"when you want the participant to draw or write on the surface themselves; use 'lock' to return the surface " +
				"to view-only mode after they finish; use 'open' to show the surface in view-only mode without drawing; " +
				"use 'close' to hide/close the surface.",
			InputSchema: objectSchema(map[string]any{
				"surface": enumProp("Which workspace surface to control.", "whiteboard", "shared_notepad", "both"),
				"action": enumProp("'unlock' lets the participant draw/write (use ONLY when it is the participant's turn to draw/write, NEVER for your own drawing), "+
					"'lock' returns it to view-only mode, 'open' shows it in view-only mode, 'close' hides it.", "unlock", "lock", "open", "close"),
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

			if action == "open" || action == "close" {
				visible := action == "open"
				if err := deps.Room.SetWorkspaceVisibility(ctx, call.RoomID, surface, visible); err != nil {
					return errorResult(call, "the surface could not be shown or hidden: %s", err.Error()), nil
				}
				return resultJSON(call, map[string]any{
					"ok":      true,
					"surface": surface,
					"action":  action,
					"visible": visible,
				}), nil
			}

			locked := false
			switch action {
			case "unlock":
			case "lock":
				locked = true
			default:
				return errorResult(call, "action must be unlock, lock, open or close"), nil
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
