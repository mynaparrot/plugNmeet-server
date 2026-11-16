package mcp

// Read-only gate and schema sanitiser for external MCP tools. A remote server
// is untrusted, so only tools that are demonstrably safe to call are exposed and
// their input schema is rebuilt from an allow-list of keywords.

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// mutatingMCPVerbs are the name tokens that mark a tool as changing state.
var mutatingMCPVerbs = map[string]struct{}{
	"create": {}, "update": {}, "delete": {}, "remove": {}, "drop": {},
	"insert": {}, "upsert": {}, "write": {}, "edit": {}, "modify": {},
	"put": {}, "post": {}, "patch": {}, "exec": {}, "execute": {},
	"run": {}, "eval": {}, "shell": {}, "bash": {}, "cmd": {},
	"deploy": {}, "send": {}, "publish": {}, "pay": {}, "transfer": {},
	"lock": {}, "unlock": {}, "reset": {}, "clear": {}, "grant": {},
	"revoke": {}, "set": {}, "add": {}, "push": {}, "commit": {},
	"merge": {}, "cancel": {}, "terminate": {}, "kill": {}, "start": {},
	"stop": {}, "restart": {}, "upload": {}, "import": {}, "export": {},
}

// externalMCPDangerousSubstrings catch obfuscated names that token matching can
// miss. They are narrowed to verbs that never appear in a read tool name, so
// names like get_settings stay allowed.
var externalMCPDangerousSubstrings = []string{
	"exec", "eval", "shell", "bash", "cmd", "deploy", "publish", "transfer",
}

// readOnlyMCPPrefixes mark a tool as a pure read.
var readOnlyMCPPrefixes = map[string]struct{}{
	"get": {}, "read": {}, "list": {}, "search": {}, "fetch": {},
	"query": {}, "find": {}, "lookup": {}, "describe": {}, "show": {},
	"view": {}, "inspect": {}, "check": {}, "count": {},
}

// isAllowedReadOnlyMCPTool decides whether a remote tool may be exposed to the
// model. It is deny-by-default: the annotations must not claim a destructive
// tool, the name must not contain a mutating verb, and the tool must either
// declare itself read-only or start with a read verb.
func isAllowedReadOnlyMCPTool(tool *mcpsdk.Tool, allowed []string) bool {
	if tool == nil || strings.TrimSpace(tool.Name) == "" {
		return false
	}
	if len(allowed) > 0 && !containsString(allowed, tool.Name) {
		return false
	}

	var readOnlyHint bool
	if annotations := tool.Annotations; annotations != nil {
		if annotations.DestructiveHint != nil && *annotations.DestructiveHint {
			return false
		}
		readOnlyHint = annotations.ReadOnlyHint
	}

	tokens := externalMCPNameTokens(tool.Name)
	if len(tokens) == 0 || hasMutatingMCPToken(tokens) {
		return false
	}
	if readOnlyHint {
		return true
	}
	_, safe := readOnlyMCPPrefixes[tokens[0]]
	return safe
}

// hasMutatingMCPToken rejects names that carry a state-changing verb.
func hasMutatingMCPToken(tokens []string) bool {
	for _, token := range tokens {
		if _, mutating := mutatingMCPVerbs[token]; mutating {
			return true
		}
	}
	compacted := strings.Join(tokens, "")
	for _, dangerous := range externalMCPDangerousSubstrings {
		if strings.Contains(compacted, dangerous) {
			return true
		}
	}
	return false
}

// externalMCPNameTokens splits a tool name on separators and camelCase
// boundaries, lowercased: "getWeatherNow" -> [get weather now].
func externalMCPNameTokens(name string) []string {
	tokens := make([]string, 0, 4)
	var current []rune
	flush := func() {
		if len(current) > 0 {
			tokens = append(tokens, strings.ToLower(string(current)))
			current = current[:0]
		}
	}

	runes := []rune(strings.TrimSpace(name))
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == '.' || r == ' ' || r == '/':
			flush()
		case unicode.IsUpper(r):
			// Break on lower->upper and on the last capital of an acronym run.
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				flush()
			} else if i > 0 && unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				flush()
			}
			current = append(current, r)
		default:
			current = append(current, r)
		}
	}
	flush()
	return tokens
}

// sanitizeExternalMCPInputSchema rebuilds a flat object schema that validateArgs
// understands, dropping everything else the remote server sent.
func sanitizeExternalMCPInputSchema(raw any) (map[string]any, error) {
	object, err := externalMCPSchemaObject(raw)
	if err != nil {
		return nil, err
	}

	sanitized := map[string]any{"type": "object", "properties": map[string]any{}}
	properties := sanitized["properties"].(map[string]any)

	rawProperties, _ := object["properties"].(map[string]any)
	names := make([]string, 0, len(rawProperties))
	for name := range rawProperties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(properties) >= MaxExternalMCPSchemaProps {
			break
		}
		prop, ok := externalMCPSanitizeProperty(rawProperties[name])
		if !ok {
			continue
		}
		properties[name] = prop
	}

	if required := externalMCPRequiredKeys(object["required"]); len(required) > 0 {
		kept := make([]string, 0, len(required))
		for _, key := range required {
			if _, ok := properties[key]; ok {
				kept = append(kept, key)
			}
		}
		if len(kept) > 0 {
			sanitized["required"] = kept
		}
	}

	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxExternalMCPSchemaBytes {
		return nil, errExternalMCPSchemaTooLarge
	}
	return sanitized, nil
}

// externalMCPSchemaObject normalises the server's schema into a JSON object.
func externalMCPSchemaObject(raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return typed, nil
	case json.RawMessage:
		object := map[string]any{}
		if err := json.Unmarshal(typed, &object); err != nil {
			return nil, errors.New("the tool schema is not an object")
		}
		return object, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	object := map[string]any{}
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, errors.New("the tool schema is not an object")
	}
	return object, nil
}

// externalMCPSanitizeProperty keeps primitive properties only.
func externalMCPSanitizeProperty(raw any) (map[string]any, bool) {
	property, err := externalMCPSchemaObject(raw)
	if err != nil || len(property) == 0 {
		return nil, false
	}

	kind, _ := property["type"].(string)
	switch kind {
	case "string", "integer", "number", "boolean":
	case "array":
		items, err := externalMCPSchemaObject(property["items"])
		if err != nil {
			return nil, false
		}
		if !isExternalMCPPrimitiveType(items["type"]) {
			return nil, false
		}
	default:
		return nil, false
	}

	clean := map[string]any{"type": kind}
	if description, _ := property["description"].(string); description != "" {
		clean["description"] = truncateRunes(description, externalMCPDescriptionChars)
	}
	if kind == "array" {
		items, _ := externalMCPSchemaObject(property["items"])
		item := map[string]any{"type": items["type"]}
		if description, _ := items["description"].(string); description != "" {
			item["description"] = truncateRunes(description, externalMCPDescriptionChars)
		}
		clean["items"] = item
	}
	if kind == "string" {
		if values, ok := externalMCPEnumValues(property["enum"]); ok {
			clean["enum"] = values
		}
	}
	return clean, true
}

func isExternalMCPPrimitiveType(raw any) bool {
	kind, _ := raw.(string)
	switch kind {
	case "string", "integer", "number", "boolean":
		return true
	}
	return false
}

// externalMCPEnumValues keeps short primitive enums, which help the model.
func externalMCPEnumValues(raw any) ([]string, bool) {
	list, ok := raw.([]any)
	if !ok {
		if values, isStrings := raw.([]string); isStrings {
			list = make([]any, 0, len(values))
			for _, value := range values {
				list = append(list, value)
			}
		} else {
			return nil, false
		}
	}
	if len(list) == 0 || len(list) > 20 {
		return nil, false
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		value, ok := item.(string)
		if !ok || utf8.RuneCountInString(value) > 64 {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func externalMCPRequiredKeys(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		keys := make([]string, 0, len(typed))
		for _, item := range typed {
			if key, ok := item.(string); ok {
				keys = append(keys, key)
			}
		}
		return keys
	}
	return nil
}
