package models

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/insights/mcp"
)

func TestMergeExternalMCPServersStampsScopes(t *testing.T) {
	merged, err := MergeExternalMCPServers(
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"docs": {Url: "https://agent.example.com/mcp"},
		},
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"lessons": {Url: "https://chat.example.com/mcp"},
		},
	)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if len(merged) != 2 {
		t.Fatalf("merged servers = %d, want 2", len(merged))
	}
	if got := merged["docs"].GetScope(); got != mcp.MCPScopeAIAgent {
		t.Errorf("docs scope = %q, want %q", got, mcp.MCPScopeAIAgent)
	}
	if got := merged["lessons"].GetScope(); got != mcp.MCPScopeTextChat {
		t.Errorf("lessons scope = %q, want %q", got, mcp.MCPScopeTextChat)
	}

	// One endpoint configured by both features becomes a shared server.
	shared, err := MergeExternalMCPServers(
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"kb": {Url: "https://kb.example.com/mcp"},
		},
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"kb": {Url: "https://kb.example.com/mcp"},
		},
	)
	if err != nil || len(shared) != 1 || shared["kb"].GetScope() != mcp.MCPScopeShared {
		t.Fatalf("shared merge = %+v (err %v)", shared, err)
	}

	// The room-wide budget is shared by both features.
	_, err = MergeExternalMCPServers(
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"a": {Url: "https://a.example.com/mcp"},
			"b": {Url: "https://b.example.com/mcp"},
		},
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"c": {Url: "https://c.example.com/mcp"},
		},
	)
	if !errors.Is(err, config.ErrAIAgentInvalidMCPServer) {
		t.Errorf("three merged servers must be refused, got %v", err)
	}

	// One key cannot point at two endpoints.
	_, err = MergeExternalMCPServers(
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"docs": {Url: "https://agent.example.com/mcp"},
		},
		map[string]*plugnmeet.ExternalMCPServerConfig{
			"docs": {Url: "https://chat.example.com/mcp"},
		},
	)
	if !errors.Is(err, config.ErrAIAgentInvalidMCPServer) {
		t.Errorf("a key collision with different endpoints must be refused, got %v", err)
	}
}

func TestPrepareRoomExternalMCPMergesTextChatServers(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	model := newAIAgentTestModel(rc)
	ctx := context.Background()
	roomId := "room-text-chat-mcp"

	agent := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:           true,
		MaxSessionSeconds: 600,
		McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{
			"docs": {
				Url:     "https://127.0.0.1/mcp",
				Headers: map[string]string{"Authorization": "Bearer agent-secret"},
			},
		},
	}
	chat := &plugnmeet.InsightsAITextChatFeatures{
		McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{
			"lessons": {
				Url:     "https://127.0.0.2/mcp",
				Headers: map[string]string{"Authorization": "Bearer chat-secret"},
			},
		},
	}

	model.PrepareRoomExternalMCP(ctx, roomId, agent, chat)

	// Both feature sets lose their servers: the metadata is broadcast to every
	// participant and the servers are only accepted while creating the room.
	if got := agent.GetMcpServers(); len(got) != 0 {
		t.Fatalf("the agent's servers must be stripped from the metadata, got %v", got)
	}
	if got := chat.GetMcpServers(); len(got) != 0 {
		t.Fatalf("the text chat's servers must be stripped from the metadata, got %v", got)
	}

	raw, err := rc.Get(ctx, aiAgentTestKey(roomId, "mcp_config")).Result()
	if err != nil {
		t.Fatalf("the merged configuration must be stored: %v", err)
	}
	stored := map[string]*plugnmeet.ExternalMCPServerConfig{}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored servers = %d, want 2 (%+v)", len(stored), stored)
	}
	if got := stored["docs"].GetScope(); got != mcp.MCPScopeAIAgent {
		t.Errorf("docs scope = %q, want %q", got, mcp.MCPScopeAIAgent)
	}
	if got := stored["lessons"].GetScope(); got != mcp.MCPScopeTextChat {
		t.Errorf("lessons scope = %q, want %q", got, mcp.MCPScopeTextChat)
	}
	if got := stored["lessons"].GetHeaders()["Authorization"]; got != "Bearer chat-secret" {
		t.Errorf("the stored server must keep its credential, got %q", got)
	}
}
