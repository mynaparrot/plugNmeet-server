package mcp

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestNoDirectRedisImports keeps every pnm:ai_agent:* key and Redis call in
// pkg/services/redis: this package must not talk to Redis itself.
func TestNoDirectRedisImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range file.Imports {
			if strings.Contains(imp.Path.Value, "redis/go-redis") {
				t.Errorf("%s must not import %s; use redisservice instead", name, imp.Path.Value)
			}
		}
	}
}
