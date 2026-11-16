package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

const (
	ToolCreateBreakoutRooms = "create_breakout_rooms"
	ToolManageBreakoutRooms = "manage_breakout_rooms"

	// Shape limits mirrored by the tool schemas.
	minBreakoutDurationMinutes = 5
	maxBreakoutDurationMinutes = 180
	minBreakoutExtendMinutes   = 1
	maxBreakoutRoomsPerCall    = 8

	// Creating child rooms seeds a room per title, so it is much slower than a
	// plain metadata update.
	breakoutToolTimeout = 25 * time.Second
)

func registerBreakoutTools(reg *Registry, deps ToolDependencies) {
	if deps.Breakout == nil {
		return
	}

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: breakoutToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolCreateBreakoutRooms,
			Description: "Create 1-8 breakout rooms and spread the participants who are in the main room evenly " +
				"across them. Every participant receives the invitation right away. Pass room_titles to name the " +
				"rooms, or room_count to only choose how many rooms to open. Say out loud which rooms you opened " +
				"and who goes where.",
			InputSchema: objectSchema(map[string]any{
				"duration_minutes": intProp("How long each breakout room stays open.", minBreakoutDurationMinutes, maxBreakoutDurationMinutes),
				"welcome_message":  stringProp("Optional message shown to the participants inside each breakout room.", 500),
				"room_titles": arrayProp("Optional titles of the rooms; the number of titles is the number of rooms.",
					stringProp("", 120), 1, maxBreakoutRoomsPerCall),
				"room_count": intProp("Optional number of rooms to open when no titles are given.", 1, maxBreakoutRoomsPerCall),
			}, "duration_minutes"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				DurationMinutes int      `json:"duration_minutes"`
				WelcomeMessage  string   `json:"welcome_message"`
				RoomTitles      []string `json:"room_titles"`
				RoomCount       int      `json:"room_count"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}
			if in.DurationMinutes < minBreakoutDurationMinutes || in.DurationMinutes > maxBreakoutDurationMinutes {
				return errorResult(call, "duration_minutes must be between %d and %d",
					minBreakoutDurationMinutes, maxBreakoutDurationMinutes), nil
			}

			titles, err := breakoutRoomTitles(in.RoomTitles, in.RoomCount)
			if err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			created, err := deps.Breakout.CreateBreakoutRooms(ctx, call.RoomID, call.AgentUserID,
				uint64(in.DurationMinutes), strings.TrimSpace(in.WelcomeMessage), titles)
			if err != nil {
				return errorResult(call, "the breakout rooms could not be created: %s", err.Error()), nil
			}

			return resultJSON(call, map[string]any{
				"ok":               true,
				"rooms_created":    created,
				"room_titles":      titles,
				"duration_minutes": in.DurationMinutes,
			}), nil
		},
	})

	reg.Register(&Tool{
		Kind:    ToolKindServer,
		Timeout: breakoutToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolManageBreakoutRooms,
			Description: "Manage the active breakout rooms: list them with their participants, send a message to all of " +
				"them, extend one room's timer, end one room, or end all of them. Listing first is the reliable way to " +
				"learn the breakout_room_id you need for the other actions.",
			InputSchema: objectSchema(map[string]any{
				"action": enumProp("What to do with the breakout rooms.",
					"list", "broadcast_message", "extend_duration", "end_room", "end_all"),
				"breakout_room_id": stringProp("The breakout room id from the list action; required for end_room and extend_duration.", 160),
				"message":          stringProp("The message to show inside every breakout room; required for broadcast_message.", 500),
				"duration_minutes": intProp("How many minutes to add; required for extend_duration.", minBreakoutExtendMinutes, maxBreakoutDurationMinutes),
			}, "action"),
		},
		Handler: func(ctx context.Context, call ToolCall) (ToolResult, error) {
			var in struct {
				Action          string `json:"action"`
				BreakoutRoomID  string `json:"breakout_room_id"`
				Message         string `json:"message"`
				DurationMinutes int    `json:"duration_minutes"`
			}
			if err := decodeArgs(call, &in); err != nil {
				return errorResult(call, "%s", err.Error()), nil
			}

			action := strings.ToLower(strings.TrimSpace(in.Action))
			breakoutRoomID := strings.TrimSpace(in.BreakoutRoomID)
			message := strings.TrimSpace(in.Message)
			switch action {
			case "list", "end_all":
			case "broadcast_message":
				if message == "" {
					return errorResult(call, "message is required for broadcast_message"), nil
				}
			case "extend_duration":
				if breakoutRoomID == "" {
					return errorResult(call, "breakout_room_id is required for extend_duration; call action=\"list\" first"), nil
				}
				if in.DurationMinutes < minBreakoutExtendMinutes {
					return errorResult(call, "duration_minutes is required for extend_duration"), nil
				}
			case "end_room":
				if breakoutRoomID == "" {
					return errorResult(call, "breakout_room_id is required for end_room; call action=\"list\" first"), nil
				}
			default:
				return errorResult(call, "action must be one of: list, broadcast_message, extend_duration, end_room, end_all"), nil
			}

			result, err := deps.Breakout.ManageBreakoutRooms(ctx, call.RoomID, action, breakoutRoomID, message,
				uint64(in.DurationMinutes))
			if err != nil {
				return errorResult(call, "the breakout rooms could not be updated: %s", err.Error()), nil
			}

			out := map[string]any{"ok": true, "action": action}
			if result != nil {
				out["rooms"] = result
			}
			return resultJSON(call, out), nil
		},
	})
}

// breakoutRoomTitles resolves the requested titles. Explicit titles win; a
// count alone gets numbered titles. Clearing both is an error, so the tool never
// guesses how many rooms the host wanted.
func breakoutRoomTitles(rawTitles []string, count int) ([]string, error) {
	titles := make([]string, 0, len(rawTitles))
	seen := make(map[string]struct{}, len(rawTitles))
	for _, title := range rawTitles {
		title = strings.TrimSpace(title)
		if title == "" {
			continue
		}
		if _, ok := seen[title]; ok {
			return nil, fmt.Errorf("room_titles must be unique, %q appears more than once", title)
		}
		seen[title] = struct{}{}
		titles = append(titles, title)
	}

	if len(titles) > 0 {
		return titles, nil
	}
	if count < 1 {
		return nil, fmt.Errorf("room_titles or room_count is required to create breakout rooms")
	}
	if count > maxBreakoutRoomsPerCall {
		return nil, fmt.Errorf("room_count must be at most %d", maxBreakoutRoomsPerCall)
	}

	titles = make([]string, 0, count)
	for i := 0; i < count; i++ {
		titles = append(titles, "Room "+strconv.Itoa(i+1))
	}
	return titles, nil
}
