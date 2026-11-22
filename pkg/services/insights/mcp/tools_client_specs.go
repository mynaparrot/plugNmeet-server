package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// Client-delegated tool names. The whiteboard and the notepad live in the
// browser, so b-server only declares them; a-client executes them.
const (
	ToolNotepadWrite = "notepad_write"
	ToolNotepadRead  = "notepad_read"

	ToolWhiteboardWriteContent    = "whiteboard_write_content"
	ToolWhiteboardDrawShapes      = "whiteboard_draw_shapes"
	ToolWhiteboardDrawFreehandSVG = "whiteboard_draw_freehand_svg"
	ToolWhiteboardDrawDiagram     = "whiteboard_draw_diagram"
	ToolWhiteboardLaserPoint      = "whiteboard_laser_point"
	ToolWhiteboardManageBoard     = "whiteboard_manage_board"
	ToolWhiteboardCaptureSnapshot = "whiteboard_capture_snapshot"

	animatedClientToolTimeout = 15 * time.Second
)

// BuildRoomToolRegistry registers every tool the room's features allow.
func BuildRoomToolRegistry(features *plugnmeet.InsightsAIAgentFeatures, deps ToolDependencies) *Registry {
	if features == nil {
		features = &plugnmeet.InsightsAIAgentFeatures{}
	}

	reg := NewRegistry()

	if features.AllowPolls {
		registerPollTools(reg, deps)
	}
	if features.AllowChatMessage {
		registerChatTools(reg, deps)
	}
	registerRoomTools(reg, deps)
	registerProgressTools(reg, deps)
	if features.AllowWebFetch {
		registerKnowledgeTools(reg, features, deps)
	}
	if features.AllowNotepadWrite {
		registerNotepadTools(reg, deps)
	}
	if features.AllowWhiteboardWrite {
		registerWhiteboardTools(reg, deps)
	}
	if features.AllowWhiteboardWrite || features.AllowNotepadWrite {
		registerWorkspaceLockTool(reg, deps)
	}
	registerExternalMCPTools(reg, deps)
	// Room-administration tools. Their backends are nil outside the real wiring,
	// so every existing registry keeps exactly the tools it had before.
	registerBreakoutTools(reg, deps)
	registerInsightsTools(reg, deps)
	registerRoomOpsTools(reg, deps)
	registerParticipantTools(reg, deps)
	for _, tool := range deps.ExtraTools {
		reg.Register(tool)
	}

	return reg
}

// BuildAdminTextChatToolRegistry is the tool set of an AI text chat session run
// by a room administrator: every room, whiteboard, notepad, poll, chat and
// administration tool plus the external MCP servers scoped to the text chat.
// A text chat session always previews a quiz before it reaches the room, so the
// quiz approval flow replaces the direct create_quiz tool. features only
// supplies the knowledge/web-fetch configuration.
func BuildAdminTextChatToolRegistry(features *plugnmeet.InsightsAIAgentFeatures, deps ToolDependencies) *Registry {
	deps.RequireQuizApproval = true
	deps.MCPScope = MCPScopeTextChat

	room := &plugnmeet.InsightsAIAgentFeatures{
		AllowPolls:           true,
		AllowChatMessage:     true,
		AllowNotepadWrite:    true,
		AllowWhiteboardWrite: true,
		AllowVisionCapture:   true,
	}
	if features != nil {
		room.AllowWebFetch = features.GetAllowWebFetch()
		room.AllowedKnowledgeUrls = features.GetAllowedKnowledgeUrls()
		room.KnowledgeCacheTtlSeconds = features.GetKnowledgeCacheTtlSeconds()
	}

	return BuildRoomToolRegistry(room, deps)
}

func registerNotepadTools(reg *Registry, deps ToolDependencies) {
	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name:        ToolNotepadRead,
			Description: "Read the shared notepad blocks separated by author (\"agent\" vs \"participant\").",
			InputSchema: objectSchema(map[string]any{}),
		},
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolNotepadWrite,
			Description: "Write or mark Markdown in the shared notepad (automatically opens and brings the shared " +
				"notepad into view). To mark mistakes inline, call notepad_read for block ids, then use " +
				"mode=\"replace_block\" with block_id (e.g. \"~~wrong~~ **correct**\").",
			InputSchema: objectSchema(map[string]any{
				"markdown": stringProp("Markdown content.", 8000),
				"mode": enumProp("Write mode: 'append', 'insert_section' (under heading), 'replace_block' (update "+
					"block_id in-place), or 'replace' (overwrite all).",
					"replace", "append", "insert_section", "replace_block"),
				"heading":  stringProp("Section heading for insert_section.", 120),
				"block_id": stringProp("Block id from notepad_read for replace_block.", 120),
			}, "markdown"),
		},
		Prepare: func(ctx context.Context, call ToolCall) error {
			if deps.Notepad == nil {
				return nil
			}
			return deps.Notepad.EnsureNotepadActive(ctx, call.RoomID)
		},
	})
}

// whiteboardPrepare brings the whiteboard into view before a whiteboard call is
// delegated, so the participant sees where the agent is drawing.
func whiteboardPrepare(deps ToolDependencies) func(ctx context.Context, call ToolCall) error {
	return func(ctx context.Context, call ToolCall) error {
		if deps.Whiteboard == nil {
			return nil
		}
		return deps.Whiteboard.EnsureWhiteboardVisible(ctx, call.RoomID)
	}
}

// prepareManageBoard reveals the whiteboard first and then applies an explicit
// unlock_board / lock_board / close_board request exactly like set_workspace_lock
// does. close_board is the exception: it must hide the board, never reveal it.
func prepareManageBoard(deps ToolDependencies) func(ctx context.Context, call ToolCall) error {
	reveal := whiteboardPrepare(deps)
	return func(ctx context.Context, call ToolCall) error {
		var in struct {
			Action string `json:"action"`
		}
		action := ""
		if err := decodeArgs(call, &in); err == nil {
			action = strings.TrimSpace(in.Action)
		}

		if action == "close_board" && deps.Room != nil {
			if err := deps.Room.SetWorkspaceLock(ctx, call.RoomID, "whiteboard", true, "all"); err != nil {
				return err
			}
			return deps.Room.SetWorkspaceVisibility(ctx, call.RoomID, "whiteboard", false)
		}

		if err := reveal(ctx, call); err != nil {
			return err
		}
		if deps.Room == nil {
			return nil
		}

		switch action {
		case "unlock_board":
			return deps.Room.SetWorkspaceLock(ctx, call.RoomID, "whiteboard", false, "all")
		case "lock_board":
			return deps.Room.SetWorkspaceLock(ctx, call.RoomID, "whiteboard", true, "all")
		}
		return nil
	}
}

// whiteboardLayoutPropDesc and whiteboardAnchorPropDesc describe the group-based
// placement model every whiteboard drawing tool shares.
const whiteboardLayoutPropDesc = "Placement: \"free_space\" (default) or \"page_center\" always starts a new section " +
	"row below existing content — use it for a new or unrelated topic/step. \"right_of_element\" or " +
	"\"left_of_element\" with anchor_element_id (the target group's anchorElementId, its groupId, or any element id " +
	"in it) places content side-by-side, so use it ONLY for related content (e.g. explanation sticky notes/cards " +
	"beside a diagram, or two side-by-side examples of the same topic). \"below_element\"/\"above_element\" stacks " +
	"directly under/above a specific group, \"inside_element\" draws inside a specific shape."

const whiteboardAnchorPropDesc = "Target element id for anchored layouts: the target group's anchorElementId, its " +
	"groupId, or any element id inside that group. With right_of_element/left_of_element/below_element/" +
	"above_element it may be omitted (or passed as \"last\"/\"latest\") to anchor to the group drawn most recently " +
	"on the page, which is what a follow-up beside your previous drawing needs."

const whiteboardGroupResultDesc = " Each call creates one visual group and returns groupId, anchorElementId, " +
	"elementIds, bbox and pageBounds."

func registerWhiteboardTools(reg *Registry, deps ToolDependencies) {
	prepare := whiteboardPrepare(deps)

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardWriteContent,
			Description: "Write text headings/notes, vocabulary cards, sticky notes, or a bordered table " +
				"(style=\"comparison_table\" with blocks=[{columns:[\"Col 1\",\"Col 2\"]}]; use \"\" for blank cells). " +
				"Placed automatically; write math in Unicode (∫, π, √, ², ≠, ≤, ≥), never LaTeX." + whiteboardGroupResultDesc +
				" All blocks in one call form a single group, so related headings/notes/cards that belong together " +
				"(e.g. a title card plus numbered explanation cards beside a diagram) go in one call, with " +
				"layout=\"right_of_element\" and anchor_element_id set to the diagram's anchorElementId or groupId.",
			InputSchema: objectSchema(map[string]any{
				"layout":            enumProp(whiteboardLayoutPropDesc, "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp(whiteboardAnchorPropDesc, 120),
				"x":                 map[string]any{"type": "number"},
				"y":                 map[string]any{"type": "number"},
				"style":             enumProp("Visual style.", "heading", "body", "vocabulary_card", "sticky_note", "comparison_table"),
				"blocks": arrayProp("Content blocks or table rows.", objectSchema(map[string]any{
					"text":     stringProp("Block text (optional when columns is set).", 400),
					"emphasis": enumProp("Text emphasis.", "none", "bold", "highlight", "underline"),
					"columns":  arrayProp("Table row cells (2-5 cells; \"\" for blank).", stringProp("", 200), 0, 5),
				}), 1, 16),
				"font_size": intProp("Font size (px).", 12, 48),
				"color":     enumProp("Colour (black for teaching; contrasting colour like red for corrections).", "black", "blue", "red", "green", "orange", "violet"),
			}, "blocks"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardDrawShapes,
			Description: "The ONLY tool for geometric shapes (rectangle, ellipse, diamond, arrow, line), " +
				"marking/underlining/circling an element via target_element_id (plus target_text for a specific word), " +
				"or connecting elements via start_element_id/end_element_id. Omit x/y for automatic placement." +
				whiteboardGroupResultDesc,
			InputSchema: objectSchema(map[string]any{
				"shapes": arrayProp("The shapes to draw.", objectSchema(map[string]any{
					"type":              enumProp("Shape type.", "rectangle", "ellipse", "diamond", "arrow", "line"),
					"label":             stringProp("Label bound to the shape.", 120),
					"x":                 map[string]any{"type": "number"},
					"y":                 map[string]any{"type": "number"},
					"width":             map[string]any{"type": "number", "minimum": 20, "maximum": 2000},
					"height":            map[string]any{"type": "number", "minimum": 20, "maximum": 2000},
					"stroke_color":      enumProp("Stroke colour (black for teaching; red for correction marks).", "black", "blue", "red", "green", "orange", "violet"),
					"background_color":  enumProp("Fill colour.", "transparent", "white", "yellow", "green", "blue", "red"),
					"fill_style":        enumProp("Fill style.", "hachure", "cross-hatch", "solid"),
					"stroke_width":      intProp("Stroke width.", 1, 4),
					"target_element_id": stringProp("Element to wrap or mark instead of x/y/width/height.", 120),
					"target_text":       stringProp("Word or phrase inside target_element_id to underline, circle or box.", 120),
					"padding":           map[string]any{"type": "number", "minimum": 4, "maximum": 64},
					"start_element_id":  stringProp("For arrows and lines: bind the start to this element.", 120),
					"end_element_id":    stringProp("For arrows and lines: bind the end to this element.", 120),
					"layout":            enumProp(whiteboardLayoutPropDesc, "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
					"anchor_element_id": stringProp(whiteboardAnchorPropDesc, 120),
				}, "type"), 1, 8),
			}, "shapes"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind:    ToolKindClient,
		Timeout: animatedClientToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardDrawFreehandSVG,
			Description: "Draw an illustration or icon from complete SVG markup (never use for basic geometric " +
				"shapes or text — use whiteboard_draw_shapes or whiteboard_write_content). Defaults to " +
				"render_as=\"image\"; pass layout=\"inside_element\" (or beside/above/below) with anchor_element_id to " +
				"place relative to an existing element. Omit x/y for automatic placement." + whiteboardGroupResultDesc,
			InputSchema: objectSchema(map[string]any{
				"svg":               stringProp("Complete SVG markup (path, line, polyline, polygon, circle, ellipse, rect, text, g).", 6000),
				"render_as":         enumProp("'image' (default): the full SVG as one clean vector image; 'strokes': animated hand-drawn strokes.", "image", "strokes"),
				"x":                 map[string]any{"type": "number"},
				"y":                 map[string]any{"type": "number"},
				"width":             map[string]any{"type": "number", "minimum": 40, "maximum": 1200},
				"height":            map[string]any{"type": "number", "minimum": 40, "maximum": 1200},
				"layout":            enumProp(whiteboardLayoutPropDesc, "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp(whiteboardAnchorPropDesc, 120),
				"animate":           boolProp("Only for render_as=strokes: animate the strokes while drawing."),
				"stroke_color":      enumProp("Stroke colour (black for teaching; red for corrections).", "black", "blue", "red", "green", "orange", "violet"),
				"stroke_width":      intProp("Stroke width.", 1, 4),
				"label":             stringProp("Label below the drawing.", 120),
			}, "svg"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardDrawDiagram,
			Description: "PRIMARY whiteboard drawing tool for structured visuals (flowchart, sequenceDiagram, " +
				"classDiagram, stateDiagram-v2, erDiagram, gantt, quadrantChart, timeline, mindmap). " +
				"Flowcharts/sequence/class become native shapes; gantt, quadrantChart, timeline and others are " +
				"inserted as an image. Pass raw Mermaid only (no fences, Unicode math instead of LaTeX) and omit " +
				"x/y for automatic placement." + whiteboardGroupResultDesc,
			InputSchema: objectSchema(map[string]any{
				"mermaid":           stringProp("Raw Mermaid markup (no fences).", 4000),
				"x":                 map[string]any{"type": "number"},
				"y":                 map[string]any{"type": "number"},
				"scale":             map[string]any{"type": "number", "minimum": 0.3, "maximum": 2.0},
				"layout":            enumProp(whiteboardLayoutPropDesc, "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp(whiteboardAnchorPropDesc, 120),
			}, "mermaid"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind:    ToolKindClient,
		Timeout: animatedClientToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardLaserPoint,
			Description: "Move a laser/cursor pointer along a path or circle target_element_id to draw attention while " +
				"speaking.",
			InputSchema: objectSchema(map[string]any{
				"motion": objectSchema(map[string]any{
					"kind":              enumProp("Motion kind.", "point", "path", "circle_element"),
					"x":                 map[string]any{"type": "number"},
					"y":                 map[string]any{"type": "number"},
					"path":              arrayProp("Path points.", objectSchema(map[string]any{"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}}, "x", "y"), 0, 12),
					"target_element_id": stringProp("Element to circle.", 120),
				}, "kind"),
				"duration_ms": intProp("Animation duration (ms).", 200, 8000),
				"hold_ms":     intProp("How long the pointer stays visible.", 0, 5000),
				"tool":        enumProp("Pointer tool.", "laser", "pointer"),
			}, "motion"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardManageBoard,
			Description: "Manage the whiteboard: clear_board (confirm=true), switch_page, switch_office_file, " +
				"zoom_to_elements, undo_last_agent_action, unlock_board (ONLY when it is the participant's turn to " +
				"draw), lock_board (view-only), or close_board.",
			InputSchema: objectSchema(map[string]any{
				"action":      enumProp("The board action.", "clear_board", "switch_page", "switch_office_file", "zoom_to_elements", "undo_last_agent_action", "unlock_board", "lock_board", "close_board"),
				"page":        minIntProp("Page to switch to.", 1),
				"file_id":     stringProp("Office file id to switch to.", 120),
				"element_ids": arrayProp("Elements to zoom to.", stringProp("", 120), 0, 50),
				"confirm":     boolProp("Must be true for clear_board."),
			}, "action"),
		},
		Prepare: prepareManageBoard(deps),
	})

	// Metadata-only inspection works even when vision capture is disabled: the
	// client then sends the element metadata without any WebP image bytes.
	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardCaptureSnapshot,
			Description: "Read whiteboard element metadata (ids, types, text, coordinates, word spans) and optional " +
				"image. Use scope=\"viewport\" (default) or \"full_canvas\", and include_image=false by default (true " +
				"only for freehand sketches; image is never sent alone).",
			InputSchema: objectSchema(map[string]any{
				"scope":         enumProp("Which part of the whiteboard to inspect.", "viewport", "full_canvas"),
				"include_image": boolProp("Also attach a WebP image next to the metadata. Defaults to false."),
				"reason":        enumProp("Why the snapshot is taken.", "tool_request", "page_change"),
				"max_edge_px":   intProp("Max image edge (px) when include_image is true.", 512, 1600),
			}),
		},
		Prepare: prepare,
	})
}
