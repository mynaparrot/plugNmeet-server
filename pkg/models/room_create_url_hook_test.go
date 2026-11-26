package models

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// newExternalURLHookConfig returns a one-shot external_url_hook script. The script
// writes the request it received to recordFile, when given, and then answers with
// answer.
func newExternalURLHookConfig(t *testing.T, recordFile, answer string) *hooks.Config {
	t.Helper()

	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if recordFile != "" {
		script += "cat > " + recordFile + "\n"
	}
	script += "echo '" + answer + "'\n"

	path := filepath.Join(dir, "external_url_hook.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &hooks.Config{ExternalUrlHook: &hooks.HookScriptConfig{
		Scripts: []hooks.HookScript{{Script: path, IsOneShot: true}},
	}}
	if err := cfg.Initialize(context.Background(), hooks.InitOptions{
		RootWorkingDir: dir,
		DefaultTimeout: 10 * time.Second,
		Logger:         logrus.New(),
	}, hooks.Category{Name: hooks.HookNameExternalUrl, Config: cfg.ExternalUrlHook}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func newExternalURLRoomModel() *RoomModel {
	return &RoomModel{
		app:          &config.AppConfig{},
		aiAgentModel: new(AIAgentModel),
		logger:       logrus.NewEntry(logrus.New()),
	}
}

// externalURLRoomRequest builds a room creation request with an enabled AI agent
// that manages its own provider, so no server-side insights config is needed.
func externalURLRoomRequest(knowledgeURLs ...string) *plugnmeet.CreateRoomReq {
	return &plugnmeet.CreateRoomReq{
		RoomId: "room-ext-url",
		Metadata: &plugnmeet.RoomMetadata{
			RoomFeatures: &plugnmeet.RoomCreateFeatures{
				InsightsFeatures: &plugnmeet.InsightsFeatures{
					IsAllow: true,
					AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
						IsAllow:              true,
						SystemPrompt:         "teach fractions",
						Topic:                "fractions",
						AllowedKnowledgeUrls: knowledgeURLs,
						CustomProvider:       &plugnmeet.InsightsAIAgentCustomProvider{Provider: "google", ApiKey: "google-key"},
					},
				},
			},
		},
	}
}

func readHookRequest(t *testing.T, recordFile string) hooks.ExternalUrlHookData {
	t.Helper()

	raw, err := os.ReadFile(recordFile)
	if err != nil {
		t.Fatalf("the external_url_hook was not called: %v", err)
	}
	var record hooks.ExternalUrlHookData
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("unmarshal hook request: %v", err)
	}
	return record
}

func TestValidateAiExternalUrlsRefusesUnusableKnowledgePrefixes(t *testing.T) {
	m := newExternalURLRoomModel()
	ctx := context.Background()

	for name, url := range map[string]string{
		"http scheme":      "http://docs.example.com/kb",
		"raw ip":           "https://1.2.3.4/docs",
		"loopback host":    "https://localhost/docs",
		"single label":     "https://intranet/docs",
		"non default port": "https://docs.example.com:8443/kb",
	} {
		t.Run(name, func(t *testing.T) {
			err := m.validateAiExternalUrls(ctx, externalURLRoomRequest(url), m.logger)
			if !errors.Is(err, config.ErrAIAgentInvalidRequest) {
				t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
			}
		})
	}

	t.Run("public prefix", func(t *testing.T) {
		req := externalURLRoomRequest("https://docs.example.com/kb")
		if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
			t.Fatalf("a public prefix must pass: %v", err)
		}
	})

	t.Run("breakout room inherits its parent", func(t *testing.T) {
		req := externalURLRoomRequest("http://docs.example.com/kb")
		req.Metadata.IsBreakoutRoom = true
		if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
			t.Fatalf("a breakout room must not re-validate its parent's prefixes: %v", err)
		}
	})

	t.Run("agent disabled", func(t *testing.T) {
		req := externalURLRoomRequest("http://docs.example.com/kb")
		req.Metadata.RoomFeatures.InsightsFeatures.AiAgentFeatures.IsAllow = false
		if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
			t.Fatalf("a disabled agent approves nothing: %v", err)
		}
	})
}

func TestValidateAiExternalUrlsRunsTheKnowledgeHook(t *testing.T) {
	m := newExternalURLRoomModel()
	ctx := context.Background()
	recordFile := filepath.Join(t.TempDir(), "knowledge-hook.json")
	m.app.Hooks = newExternalURLHookConfig(t, recordFile, "{}")

	req := externalURLRoomRequest("https://docs.example.com/kb")
	if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	record := readHookRequest(t, recordFile)
	if record.Stage != hooks.ExternalUrlHookStageValidate {
		t.Errorf("stage = %q, want %q", record.Stage, hooks.ExternalUrlHookStageValidate)
	}
	if record.Type != hooks.ExternalUrlHookTypeKnowledge {
		t.Errorf("type = %q, want %q", record.Type, hooks.ExternalUrlHookTypeKnowledge)
	}
	if record.RoomId != req.RoomId {
		t.Errorf("room id = %q, want %q", record.RoomId, req.RoomId)
	}
	if record.Url != "https://docs.example.com/kb" {
		t.Errorf("url = %q, want the approved prefix", record.Url)
	}
	if len(record.AllowedUrls) != 1 || record.AllowedUrls[0] != "https://docs.example.com/kb" {
		t.Errorf("allowed urls = %v, want the room's prefixes", record.AllowedUrls)
	}

	// a script that refuses the room rejects the creation with its own error and
	// is never reported as an invalid request
	m.app.Hooks = newExternalURLHookConfig(t, "", `{"error":"denied by policy"}`)
	err := m.validateAiExternalUrls(ctx, req, m.logger)
	if err == nil {
		t.Fatal("a refusing hook must reject the room")
	}
	if errors.Is(err, config.ErrAIAgentInvalidRequest) {
		t.Errorf("err = %v, must not be reported as an invalid request", err)
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("err = %v, want the hook's reason", err)
	}
}

func TestValidateAiExternalUrlsRunsTheAIAgentMCPHook(t *testing.T) {
	m := newExternalURLRoomModel()
	ctx := context.Background()

	req := externalURLRoomRequest()
	req.Metadata.RoomFeatures.InsightsFeatures.AiAgentFeatures.McpServers = map[string]*plugnmeet.ExternalMCPServerConfig{
		"docs": {Url: "https://mcp.example.com/mcp"},
	}

	recordFile := filepath.Join(t.TempDir(), "mcp-hook.json")
	m.app.Hooks = newExternalURLHookConfig(t, recordFile, "{}")
	if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	record := readHookRequest(t, recordFile)
	if record.Stage != hooks.ExternalUrlHookStageValidate {
		t.Errorf("stage = %q, want %q", record.Stage, hooks.ExternalUrlHookStageValidate)
	}
	if record.Type != hooks.ExternalUrlHookTypeMCP {
		t.Errorf("type = %q, want %q", record.Type, hooks.ExternalUrlHookTypeMCP)
	}
	if record.ServerKey != "docs" {
		t.Errorf("server key = %q, want docs", record.ServerKey)
	}
	if record.Url != "https://mcp.example.com/mcp" {
		t.Errorf("url = %q, want the configured endpoint", record.Url)
	}
	if record.RoomId != req.RoomId {
		t.Errorf("room id = %q, want %q", record.RoomId, req.RoomId)
	}

	m.app.Hooks = newExternalURLHookConfig(t, "", `{"error":"denied by policy"}`)
	err := m.validateAiExternalUrls(ctx, req, m.logger)
	if err == nil {
		t.Fatal("a refusing hook must reject the room")
	}
	if errors.Is(err, config.ErrAIAgentInvalidMCPServer) {
		t.Errorf("err = %v, must not be reported as an invalid mcp server", err)
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("err = %v, want the hook's reason", err)
	}
}

func TestValidateAiExternalUrlsRunsTheTextChatMCPHook(t *testing.T) {
	m := newExternalURLRoomModel()
	ctx := context.Background()
	req := &plugnmeet.CreateRoomReq{
		RoomId: "room-text-chat-hook",
		Metadata: &plugnmeet.RoomMetadata{
			RoomFeatures: &plugnmeet.RoomCreateFeatures{
				InsightsFeatures: &plugnmeet.InsightsFeatures{
					IsAllow: true,
					AiFeatures: &plugnmeet.InsightsAIFeatures{
						IsAllow: true,
						AiTextChatFeatures: &plugnmeet.InsightsAITextChatFeatures{
							IsAllow: true,
							McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{
								"kb": {Url: "https://kb.example.com/mcp"},
							},
						},
					},
				},
			},
		},
	}

	recordFile := filepath.Join(t.TempDir(), "text-chat-hook.json")
	m.app.Hooks = newExternalURLHookConfig(t, recordFile, "{}")
	if err := m.validateAiExternalUrls(ctx, req, m.logger); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}

	record := readHookRequest(t, recordFile)
	if record.Stage != hooks.ExternalUrlHookStageValidate || record.Type != hooks.ExternalUrlHookTypeMCP {
		t.Errorf("record = %+v, want the validate stage of an mcp url", record)
	}
	if record.ServerKey != "kb" || record.RoomId != req.RoomId {
		t.Errorf("record = %+v, want the room's server key", record)
	}

	m.app.Hooks = newExternalURLHookConfig(t, "", `{"error":"denied by policy"}`)
	if err := m.validateAiExternalUrls(ctx, req, m.logger); err == nil {
		t.Fatal("a refusing hook must reject the room")
	}
}
