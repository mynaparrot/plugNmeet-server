package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// JSON-Schema builders. Tool schemas are written as map literals for the parts
// that need prose; these helpers cover the repetitive shape.

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProp(description string, maxLength int) map[string]any {
	prop := map[string]any{"type": "string", "description": description}
	if maxLength > 0 {
		prop["maxLength"] = maxLength
	}
	return prop
}

func enumProp(description string, values ...string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
		"enum":        values,
	}
}

func intProp(description string, min, max int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     min,
		"maximum":     max,
	}
}

func minIntProp(description string, min int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     min,
	}
}

func boolProp(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func arrayProp(description string, items any, minItems, maxItems int) map[string]any {
	prop := map[string]any{
		"type":        "array",
		"description": description,
		"items":       items,
	}
	if minItems > 0 {
		prop["minItems"] = minItems
	}
	if maxItems > 0 {
		prop["maxItems"] = maxItems
	}
	return prop
}

// validateArgs is a model-facing guard: it checks the required keys plus the
// primitive type, enum and size constraints of the top-level properties. Nested
// item schemas are left to the handlers. Messages are written for the model.
func validateArgs(spec *insights.MCPToolSpec, argsJSON string) error {
	if spec == nil {
		return nil
	}
	raw := strings.TrimSpace(argsJSON)
	if raw == "" {
		raw = "{}"
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return fmt.Errorf("arguments must be a JSON object")
	}

	properties, _ := spec.InputSchema["properties"].(map[string]any)
	for _, key := range requiredKeys(spec.InputSchema) {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("missing required argument %q", key)
		}
	}
	for key, value := range args {
		prop, ok := properties[key].(map[string]any)
		if !ok {
			continue // unknown arguments are ignored, not rejected
		}
		if err := validateValue(key, prop, value); err != nil {
			return err
		}
	}
	return nil
}

func requiredKeys(schema map[string]any) []string {
	switch required := schema["required"].(type) {
	case []string:
		return required
	case []any:
		keys := make([]string, 0, len(required))
		for _, item := range required {
			if key, ok := item.(string); ok {
				keys = append(keys, key)
			}
		}
		return keys
	}
	return nil
}

func validateValue(name string, prop map[string]any, raw json.RawMessage) error {
	switch prop["type"] {
	case "string":
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%s must be a string", name)
		}
		if values, ok := stringList(prop["enum"]); ok && !containsString(values, value) {
			return fmt.Errorf("%s must be one of: %s", name, strings.Join(values, ", "))
		}
		length := utf8.RuneCountInString(value)
		if min, ok := intValue(prop["minLength"]); ok && length < min {
			return fmt.Errorf("%s must be at least %d characters", name, min)
		}
		if max, ok := intValue(prop["maxLength"]); ok && length > max {
			return fmt.Errorf("%s must be at most %d characters", name, max)
		}
	case "integer":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil || value != math.Trunc(value) {
			return fmt.Errorf("%s must be an integer", name)
		}
		return validateRange(name, value, prop)
	case "number":
		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%s must be a number", name)
		}
		return validateRange(name, value, prop)
	case "boolean":
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%s must be true or false", name)
		}
	case "array":
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("%s must be an array", name)
		}
		if min, ok := intValue(prop["minItems"]); ok && len(items) < min {
			return fmt.Errorf("%s must have at least %d items", name, min)
		}
		if max, ok := intValue(prop["maxItems"]); ok && len(items) > max {
			return fmt.Errorf("%s must have at most %d items", name, max)
		}
	case "object":
		var value map[string]json.RawMessage
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("%s must be an object", name)
		}
	}
	return nil
}

func validateRange(name string, value float64, prop map[string]any) error {
	if min, ok := intValue(prop["minimum"]); ok && value < float64(min) {
		return fmt.Errorf("%s must be at least %d", name, min)
	}
	if max, ok := intValue(prop["maximum"]); ok && value > float64(max) {
		return fmt.Errorf("%s must be at most %d", name, max)
	}
	return nil
}

func stringList(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return values, len(values) > 0
	case []any:
		out := make([]string, 0, len(values))
		for _, item := range values {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, len(out) > 0
	}
	return nil, false
}

func intValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
