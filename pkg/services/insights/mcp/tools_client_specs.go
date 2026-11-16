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
			Name: ToolNotepadRead,
			Description: "Read the current contents of the room's shared notepad, with blocks separated into what you " +
				"(the AI agent, author=\"agent\") wrote versus what the participant (author=\"participant\") wrote or " +
				"edited. Use this to check the participant's written exercises, notes, or answers in the shared notepad " +
				"and give feedback. Calling this tool automatically opens the shared notepad and brings it into view " +
				"for the room.",
			InputSchema: objectSchema(map[string]any{}),
		},
		Prepare: func(ctx context.Context, call ToolCall) error {
			if deps.Notepad == nil {
				return nil
			}
			return deps.Notepad.EnsureNotepadActive(ctx, call.RoomID)
		},
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolNotepadWrite,
			Description: "Write or mark content in the room's shared notepad using Markdown (**bold**, *italic*, " +
				"~~strikethrough~~, <u>underline</u>, `code`, lists, headings, tables). Calling this tool automatically " +
				"opens and brings the shared notepad into view. To check and mark mistakes in a participant's answer, " +
				"first call notepad_read to get the block id, then call notepad_write with mode=\"replace_block\" and " +
				"block_id to highlight mistakes inline (e.g. \"~~wrong~~ **correct**\" or \"<u>underlined</u>\"), or " +
				"mode=\"append\" to add feedback below.",
			InputSchema: objectSchema(map[string]any{
				"markdown": stringProp("The markdown content to write.", 8000),
				"mode": enumProp("How the Markdown content is applied: 'append' adds to the end, 'insert_section' "+
					"appends under heading, 'replace_block' replaces a single block_id in-place (ideal for marking "+
					"mistakes in a participant's answer), 'replace' replaces the entire note.",
					"replace", "append", "insert_section", "replace_block"),
				"heading":  stringProp("Optional section heading.", 120),
				"block_id": stringProp("Required when mode is replace_block: the id of the block from notepad_read to update in-place.", 120),
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
// unlock_board / lock_board request exactly like set_workspace_lock does.
func prepareManageBoard(deps ToolDependencies) func(ctx context.Context, call ToolCall) error {
	reveal := whiteboardPrepare(deps)
	return func(ctx context.Context, call ToolCall) error {
		if err := reveal(ctx, call); err != nil {
			return err
		}
		if deps.Room == nil {
			return nil
		}

		var in struct {
			Action string `json:"action"`
		}
		if err := decodeArgs(call, &in); err != nil {
			// The handler reports malformed arguments.
			return nil
		}

		switch strings.TrimSpace(in.Action) {
		case "unlock_board":
			return deps.Room.SetWorkspaceLock(ctx, call.RoomID, "whiteboard", false, "all")
		case "lock_board":
			return deps.Room.SetWorkspaceLock(ctx, call.RoomID, "whiteboard", true, "all")
		}
		return nil
	}
}

func registerWhiteboardTools(reg *Registry, deps ToolDependencies) {
	prepare := whiteboardPrepare(deps)

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardWriteContent,
			Description: "Write plain text headings/notes, vocabulary cards, sticky notes, or a bordered multi-column " +
				"comparison table (style=\"comparison_table\" with columns per row) on the whiteboard. Layout is " +
				"automatic and avoids existing elements; never compute pixel coordinates. For flowcharts, processes, " +
				"trees, or relational diagrams, prefer whiteboard_draw_diagram (Mermaid) instead. Use color=black for " +
				"normal teaching content; when writing a correction or suggestion beside a participant's element, use " +
				"red or another colour that contrasts with the participant's element colour. The whiteboard already " +
				"renders all text in a handwritten font, so write math formulas or equations using clean Unicode " +
				"symbols (∫, π, λ, ∑, √, ², ³, ≠, ≤, ≥, ±, ∞, e.g. \"e^(iπ) + 1 = 0\") instead of raw $$...$$ LaTeX.",
			InputSchema: objectSchema(map[string]any{
				"layout": enumProp("Where to place the content relative to the page or to anchor_element_id.", "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp(
					"Required when layout is inside_element, below_element, above_element, right_of_element, or left_of_element.", 120),
				"style": enumProp("What to render.", "heading", "body", "vocabulary_card", "sticky_note", "comparison_table"),
				"blocks": arrayProp("The blocks of content.", objectSchema(map[string]any{
					"text":     stringProp("The text of the block.", 400),
					"emphasis": enumProp("Emphasis of the block.", "none", "bold", "highlight", "underline"),
					"columns":  arrayProp("Columns for a table row (2 to 5 cells).", stringProp("", 200), 0, 5),
				}, "text"), 1, 12),
				"font_size": intProp("Font size in pixels.", 12, 48),
				"color":     enumProp("Colour: black for normal teaching content, red (or a colour contrasting with the participant's element colour) for corrections beside a participant's work.", "black", "blue", "red", "green", "orange", "violet"),
			}, "blocks"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardDrawShapes,
			Description: "Draw a simple geometric shape, or mark/underline/circle an existing element using " +
				"target_element_id (and optional target_text to mark a specific word/mistake inside that text element " +
				"without guessing coordinates), or connect two existing elements with start_element_id/end_element_id. " +
				"To place a shape relative to an existing element (inside it, beside it, above or below it), pass " +
				"layout together with anchor_element_id from whiteboard_capture_snapshot; with " +
				"target_element_id plus layout=\"inside_element\" the shape is centred and fitted inside that element. " +
				"This is the ONLY tool for geometric shapes: for a rectangle, ellipse/circle, diamond, arrow, or line " +
				"always use it (e.g. shapes=[{type:\"rectangle\", width:240, height:140, label:\"...\"}]) and never " +
				"whiteboard_draw_freehand_svg. Do NOT use this tool to build flowcharts, diagrams, or tables — always " +
				"use whiteboard_draw_diagram (Mermaid) or whiteboard_write_content (comparison_table) instead. Omit x and y so shapes are placed " +
				"automatically within the A4 width without overlapping existing content. Use stroke_color=black for " +
				"normal teaching content; when marking a mistake or drawing a correction beside a participant's " +
				"element, use red or another colour that contrasts with the participant's element colour.",
			InputSchema: objectSchema(map[string]any{
				"shapes": arrayProp("The shapes to draw.", objectSchema(map[string]any{
					"type":              enumProp("Shape type.", "rectangle", "ellipse", "diamond", "arrow", "line"),
					"label":             stringProp("Optional label bound to the shape.", 120),
					"x":                 map[string]any{"type": "number"},
					"y":                 map[string]any{"type": "number"},
					"width":             map[string]any{"type": "number", "minimum": 20, "maximum": 2000},
					"height":            map[string]any{"type": "number", "minimum": 20, "maximum": 2000},
					"stroke_color":      enumProp("Stroke colour: black for normal teaching content, red (or a colour contrasting with the participant's element colour) for mistake marks and corrections.", "black", "blue", "red", "green", "orange", "violet"),
					"background_color":  enumProp("Fill colour.", "transparent", "white", "yellow", "green", "blue", "red"),
					"fill_style":        enumProp("Fill style.", "hachure", "cross-hatch", "solid"),
					"stroke_width":      intProp("Stroke width.", 1, 4),
					"target_element_id": stringProp("Wrap this existing element's bounding box instead of using x/y/width/height.", 120),
					"target_text":       stringProp("Optional word or phrase inside target_element_id to underline, circle, or box (uses exact word coordinates from metadata).", 120),
					"padding":           map[string]any{"type": "number", "minimum": 4, "maximum": 64},
					"start_element_id":  stringProp("For arrows and lines: bind the start to this element.", 120),
					"end_element_id":    stringProp("For arrows and lines: bind the end to this element.", 120),
					"layout":            enumProp("Where to place this shape relative to anchor_element_id, target_element_id, or the page.", "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
					"anchor_element_id": stringProp("Element id to place this shape inside, beside, above, or below.", 120),
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
			Description: "Draw a picture or illustration (a house, tree, animal, object, or icon) from SVG markup, " +
				"typically when whiteboard_draw_diagram (Mermaid) cannot express it. By default the complete SVG is " +
				"inserted as one clean image, so every stroke, fill and text in the markup survives. Set " +
				"render_as=\"strokes\" only when the drawing should look hand-drawn. Never use this tool for basic " +
				"geometric shapes, text, or math formulas/equations: the whiteboard already renders text in a " +
				"handwritten font, so use whiteboard_write_content or whiteboard_draw_shapes for text and equations. " +
				"To draw an illustration inside, beside, above, or below an existing " +
				"shape (e.g. a sun inside a participant's circle), pass layout=\"inside_element\" (or " +
				"\"right_of_element\", \"left_of_element\", \"above_element\", \"below_element\") with anchor_element_id " +
				"from whiteboard_capture_snapshot: the drawing is then fitted and centred against that element. Omit x " +
				"and y so the drawing is placed automatically in free space without overlapping existing elements.",
			InputSchema: objectSchema(map[string]any{
				"svg":               stringProp("Complete SVG markup: path, line, polyline, polygon, circle, ellipse, rect, text and group elements are all rendered.", 6000),
				"render_as":         enumProp("How to put the SVG on the board: 'image' (default, renders the full SVG cleanly as a crisp vector image — best for illustrations like a house, tree, animal, or complex icon) or 'strokes' (converts primitives into animated hand-drawn strokes).", "image", "strokes"),
				"x":                 map[string]any{"type": "number"},
				"y":                 map[string]any{"type": "number"},
				"width":             map[string]any{"type": "number", "minimum": 40, "maximum": 1200},
				"height":            map[string]any{"type": "number", "minimum": 40, "maximum": 1200},
				"layout":            enumProp("Where to place the drawing relative to the page or to anchor_element_id.", "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp("Target element id from whiteboard_capture_snapshot when layout is inside_element, right_of_element, left_of_element, above_element, or below_element (e.g. to draw a sun inside a participant's circle).", 120),
				"animate":           boolProp("Only for render_as=strokes: animate the strokes while drawing."),
				"stroke_color":      enumProp("Stroke colour: black for normal teaching content, red (or a colour contrasting with the participant's element colour) for corrections.", "black", "blue", "red", "green", "orange", "violet"),
				"stroke_width":      intProp("Stroke width.", 1, 4),
				"label":             stringProp("Optional label below the drawing.", 120),
			}, "svg"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind: ToolKindClient,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardDrawDiagram,
			Description: "PRIMARY whiteboard drawing tool — use this for almost any structured visual on the whiteboard: " +
				"flowcharts, processes, hierarchies, grammar trees, timelines, mind maps, comparisons, entity/table " +
				"relationships, sequence flows, class structures, or state machines. Supported Mermaid types: " +
				"flowchart, sequenceDiagram, classDiagram, stateDiagram-v2, erDiagram, gantt, quadrantChart, and " +
				"timeline — flowcharts, sequence, and class diagrams are converted to native Excalidraw elements, " +
				"while gantt, quadrant, timeline, mind map, and other Mermaid diagram types are automatically inserted " +
				"as an image on the whiteboard. Pass ONLY raw Mermaid syntax (no markdown fences, HTML, or init " +
				"directives) with unique alphanumeric node IDs and short readable labels. Never pass " +
				"x or y unless placing at a known coordinate; omitting x and y automatically places the diagram in free " +
				"space without overlapping existing elements. To place the diagram relative to an existing element, " +
				"pass anchor_element_id with layout instead of guessing coordinates. In node and edge labels use " +
				"clean Unicode symbols (∫, π, λ, ∑, √, ², ≠, ≤, ≥) instead of raw $$...$$ LaTeX.",
			InputSchema: objectSchema(map[string]any{
				"mermaid":           stringProp("Mermaid markup.", 4000),
				"x":                 map[string]any{"type": "number"},
				"y":                 map[string]any{"type": "number"},
				"scale":             map[string]any{"type": "number", "minimum": 0.3, "maximum": 2.0},
				"layout":            enumProp("Where to place the diagram relative to the page or to anchor_element_id.", "free_space", "inside_element", "below_element", "above_element", "right_of_element", "left_of_element", "page_center"),
				"anchor_element_id": stringProp("Element id to place the diagram inside, beside, above, or below.", 120),
			}, "mermaid"),
		},
		Prepare: prepare,
	})

	reg.Register(&Tool{
		Kind:    ToolKindClient,
		Timeout: animatedClientToolTimeout,
		Spec: &insights.MCPToolSpec{
			Name: ToolWhiteboardLaserPoint,
			Description: "Point at things while you speak: move your cursor along a path or circle a specific element. " +
				"Use it instead of drawing when you only want to draw attention.",
			InputSchema: objectSchema(map[string]any{
				"motion": objectSchema(map[string]any{
					"kind":              enumProp("Motion kind.", "point", "path", "circle_element"),
					"x":                 map[string]any{"type": "number"},
					"y":                 map[string]any{"type": "number"},
					"path":              arrayProp("Points of the path.", objectSchema(map[string]any{"x": map[string]any{"type": "number"}, "y": map[string]any{"type": "number"}}, "x", "y"), 0, 12),
					"target_element_id": stringProp("Element to circle.", 120),
				}, "kind"),
				"duration_ms": intProp("Animation duration in milliseconds.", 200, 8000),
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
			Description: "Manage the board itself: clear it, switch page or file, zoom to elements, undo your own " +
				"last action, or unlock_board so a participant can draw on it. clear_board requires confirm true; say " +
				"out loud that you are clearing the board first.",
			InputSchema: objectSchema(map[string]any{
				"action":      enumProp("The board action.", "clear_board", "switch_page", "switch_office_file", "zoom_to_elements", "undo_last_agent_action", "unlock_board", "lock_board"),
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
			Description: "Read the whiteboard's structured element metadata (ids, types, text, exact " +
				"x/y/width/height, font_size, word-level coordinates, and A4 page bounds) and optionally capture a " +
				"visual snapshot. Choose scope=\"viewport\" (default, cheaper/faster) to read only the elements " +
				"currently visible in the participant's view, or scope=\"full_canvas\" to read the entire A4 page (up " +
				"to 3 pages vertically). Set include_image=false (default) for fast metadata-only inspection of text, " +
				"shapes, and coordinates; set include_image=true only when you need to visually inspect freehand " +
				"sketches or drawings (image is always paired with metadata, never sent alone).",
			InputSchema: objectSchema(map[string]any{
				"scope": enumProp("Which part of the whiteboard to inspect: 'viewport' for current visible view only "+
					"(default, lower cost) or 'full_canvas' for the entire page.", "viewport", "full_canvas"),
				"include_image": boolProp("Whether to also render and attach a WebP image alongside the element metadata. Defaults to false (metadata only)."),
				"reason":        enumProp("Why the snapshot is taken.", "tool_request", "page_change"),
				"max_edge_px":   intProp("Maximum edge of the exported image in pixels when include_image is true.", 512, 1600),
			}),
		},
		Prepare: prepare,
	})
}
