package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func TestNormalizeMCPScope(t *testing.T) {
	for raw, want := range map[string]string{
		"":            MCPScopeShared,
		"   ":         MCPScopeShared,
		"shared":      MCPScopeShared,
		"text_chat":   MCPScopeTextChat,
		" Text-Chat ": MCPScopeTextChat,
		"ai_agent":    MCPScopeAIAgent,
		"AI-AGENT":    MCPScopeAIAgent,
		"something":   MCPScopeShared,
	} {
		if got := NormalizeMCPScope(raw); got != want {
			t.Errorf("NormalizeMCPScope(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestMCPScopeAllows(t *testing.T) {
	cases := []struct {
		server, consumer string
		want             bool
	}{
		{MCPScopeAIAgent, "", true},
		{MCPScopeShared, MCPScopeAIAgent, true},
		{"", MCPScopeTextChat, true},
		{MCPScopeAIAgent, MCPScopeAIAgent, true},
		{MCPScopeTextChat, MCPScopeTextChat, true},
		{MCPScopeAIAgent, MCPScopeTextChat, false},
		{MCPScopeTextChat, MCPScopeAIAgent, false},
	}
	for _, tc := range cases {
		if got := mcpScopeAllows(tc.server, tc.consumer); got != tc.want {
			t.Errorf("mcpScopeAllows(%q, %q) = %v, want %v", tc.server, tc.consumer, got, tc.want)
		}
	}
}

func TestExternalMCPRegistryRespectsScopes(t *testing.T) {
	store := newTestRedis(t)
	ctx := context.Background()

	// The room budget is two servers, so each phase warms one scoped server
	// next to a shared one.
	type phase struct {
		roomID string
		scoped string
	}
	phases := []phase{
		{roomID: "room-scope-agent", scoped: MCPScopeAIAgent},
		{roomID: "room-scope-chat", scoped: MCPScopeTextChat},
	}

	for _, p := range phases {
		scopedFixture := newTestMCPServer(t, nil)
		scopedFixture.addTool("scoped_tool", "A scoped tool.", "scoped answer")
		sharedFixture := newTestMCPServer(t, nil)
		sharedFixture.addTool("shared_tool", "A shared tool.", "shared answer")

		scopedScope, sharedScope := p.scoped, MCPScopeShared
		servers := map[string]*plugnmeet.ExternalMCPServerConfig{
			"scoped": {Url: scopedFixture.http.URL, Scope: &scopedScope},
			"shared": {Url: sharedFixture.http.URL, Scope: &sharedScope},
		}
		manager := NewExternalMCPManager(store, testExternalMCPDeps(store, p.roomID))
		if cached := manager.Warm(ctx, p.roomID, servers, 600); len(cached) != 2 {
			t.Fatalf("%s: warmed tools = %d, want 2", p.roomID, len(cached))
		}

		names := func(scope string) string {
			deps := testExternalMCPDeps(store, p.roomID)
			deps.MCPScope = scope
			reg := BuildRoomToolRegistry(&plugnmeet.InsightsAIAgentFeatures{}, deps)
			out := make([]string, 0, 2)
			for _, spec := range reg.Specs() {
				if strings.HasPrefix(spec.Name, "ext_") {
					out = append(out, spec.Name)
				}
			}
			return strings.Join(out, ",")
		}

		// The owning consumer sees both, the other consumer only the shared one.
		if got, want := names(p.scoped), "ext_scoped_scoped_tool,ext_shared_shared_tool"; got != want {
			t.Errorf("%s: owning tools = %q, want %q", p.roomID, got, want)
		}
		other := MCPScopeAIAgent
		if p.scoped == MCPScopeAIAgent {
			other = MCPScopeTextChat
		}
		if got, want := names(other), "ext_shared_shared_tool"; got != want {
			t.Errorf("%s: other consumer tools = %q, want %q", p.roomID, got, want)
		}
		// An unscoped consumer (older wiring) still sees everything.
		if got, want := len(strings.Split(names(""), ",")), 2; got != want {
			t.Errorf("%s: unscoped tools = %d, want %d", p.roomID, got, want)
		}
	}
}

func TestUpdateRoomScopedExternalMCP(t *testing.T) {
	store := newTestRedis(t)
	ctx := context.Background()
	roomID := "room-scope-update"
	logger := logrus.NewEntry(logrus.New())

	agentFixture := newTestMCPServer(t, nil)
	agentFixture.addTool("agent_tool", "Agent tool.", "agent")
	firstChatFixture := newTestMCPServer(t, nil)
	firstChatFixture.addTool("first_tool", "First chat tool.", "first")
	secondChatFixture := newTestMCPServer(t, nil)
	secondChatFixture.addTool("second_tool", "Second chat tool.", "second")

	// The production warm-up keeps the https-only policy, so the httptest
	// endpoints are stored but their discovery fails; this test only exercises
	// the stored configuration.
	agentServers := map[string]*plugnmeet.ExternalMCPServerConfig{
		"docs": {Url: agentFixture.http.URL},
	}
	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeAIAgent, agentServers, 600, logger)

	chatServers := map[string]*plugnmeet.ExternalMCPServerConfig{
		"lessons": {Url: firstChatFixture.http.URL},
	}
	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeTextChat, chatServers, 600, logger)

	stored, err := store.LoadExternalMCPConfig(ctx, roomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored servers = %d, want 2 (%+v)", len(stored), stored)
	}
	if got := NormalizeMCPScope(stored["docs"].GetScope()); got != MCPScopeAIAgent {
		t.Errorf("the agent's server scope = %q, want %q", got, MCPScopeAIAgent)
	}
	if got := NormalizeMCPScope(stored["lessons"].GetScope()); got != MCPScopeTextChat {
		t.Errorf("the text chat's server scope = %q, want %q", got, MCPScopeTextChat)
	}

	// A text chat update replaces only its own servers.
	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeTextChat, map[string]*plugnmeet.ExternalMCPServerConfig{
		"lessons2": {Url: secondChatFixture.http.URL},
	}, 600, logger)
	stored, _ = store.LoadExternalMCPConfig(ctx, roomID)
	if len(stored) != 2 || stored["docs"] == nil || stored["lessons2"] == nil || stored["lessons"] != nil {
		t.Fatalf("stored servers after the text chat update = %+v", stored)
	}

	// Removing every text chat server leaves the agent's servers configured.
	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeTextChat, nil, 600, logger)
	stored, _ = store.LoadExternalMCPConfig(ctx, roomID)
	if len(stored) != 1 || stored["docs"] == nil {
		t.Fatalf("stored servers after clearing the text chat = %+v", stored)
	}

	// Clearing the agent's servers drops the whole room state.
	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeAIAgent, nil, 600, logger)
	if stored, _ = store.LoadExternalMCPConfig(ctx, roomID); len(stored) != 0 {
		t.Errorf("stored servers after clearing everything = %+v", stored)
	}
	if tools, err := store.LoadExternalMCPTools(ctx, roomID); err != nil || len(tools) != 0 {
		t.Errorf("the room tools must be cleared (err %v, tools %+v)", err, tools)
	}
}

func TestUpdateRoomScopedExternalMCPRestampsScopeAndCapsServers(t *testing.T) {
	store := newTestRedis(t)
	ctx := context.Background()
	roomID := "room-scope-cap"
	logger := logrus.NewEntry(logrus.New())

	fixtures := make([]*testMCPServer, 0, 3)
	servers := map[string]*plugnmeet.ExternalMCPServerConfig{}
	for i := 0; i < 3; i++ {
		fixture := newTestMCPServer(t, nil)
		fixture.addTool(fmt.Sprintf("tool_%d", i), "tool", "answer")
		fixtures = append(fixtures, fixture)
		servers[fmt.Sprintf("server_%d", i)] = &plugnmeet.ExternalMCPServerConfig{
			Url:          fixture.http.URL,
			AllowedTools: []string{"one", "two", "three", "four", "five", "six"},
		}
	}

	UpdateRoomScopedExternalMCP(ctx, store, roomID, MCPScopeAIAgent, servers, 600, logger)

	stored, err := store.LoadExternalMCPConfig(ctx, roomID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != MaxExternalMCPServersPerRoom {
		t.Fatalf("stored servers = %d, want the cap of %d", len(stored), MaxExternalMCPServersPerRoom)
	}
	for key, cfg := range stored {
		if got := NormalizeMCPScope(cfg.GetScope()); got != MCPScopeAIAgent {
			t.Errorf("%s scope = %q, want it stamped with %q", key, got, MCPScopeAIAgent)
		}
		if got := len(cfg.GetAllowedTools()); got != MaxToolsPerExternalMCPServer {
			t.Errorf("%s allowed tools = %d, want the cap of %d", key, got, MaxToolsPerExternalMCPServer)
		}
	}
}
