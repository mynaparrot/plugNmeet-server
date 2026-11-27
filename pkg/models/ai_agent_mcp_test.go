package models

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

func newAIAgentTestRedis(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// aiAgentTestKey mirrors the room-scoped Redis keys of the ai_agent and mcp
// packages.
func aiAgentTestKey(roomId, suffix string) string {
	return "pnm:ai_agent:" + roomId + ":" + suffix
}

// newAIAgentTestModel returns the model the ai agent tests share: an in-memory
// Redis and a NATS service that finds no room, so nothing needs a NATS server.
func newAIAgentTestModel(rc redis.UniversalClient) *AIAgentModel {
	ttl := 2 * time.Hour
	logger := logrus.New()
	app := &config.AppConfig{
		RedisInfo: connections.RedisInfo{DefaultTTL: &ttl},
		NatsInfo:  config.NatsInfo{DefaultTTL: &ttl},
	}
	return &AIAgentModel{
		ctx:          context.Background(),
		appConfig:    app,
		rds:          rc,
		redisService: redisservice.New(redisservice.Args{Ctx: context.Background(), Rc: rc, App: app, Logger: logger}),
		natsService:  newOfflineNatsService(),
		artifactModel: &ArtifactModel{
			app: app,
			log: logger.WithField("model", "artifact"),
		},
		logger: logrus.NewEntry(logger),
	}
}

// newOfflineNatsService builds a NATS service for the tests that run without a
// NATS server.
func newOfflineNatsService() *natsservice.NatsService {
	ttl := 2 * time.Hour
	return natsservice.New(natsservice.Args{
		Ctx:    context.Background(),
		App:    &config.AppConfig{NatsInfo: config.NatsInfo{DefaultTTL: &ttl}},
		Js:     offlineJetStream{},
		Logger: logrus.New(),
	})
}

// offlineJetStream stands in for the JetStream client in unit tests, which run
// without a NATS server: no key-value bucket exists, so no room metadata is
// found, and a publish is acknowledged without a stream behind it.
type offlineJetStream struct{ jetstream.JetStream }

func (offlineJetStream) KeyValue(context.Context, string) (jetstream.KeyValue, error) {
	return nil, jetstream.ErrBucketNotFound
}

func (offlineJetStream) Publish(context.Context, string, []byte, ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	return &jetstream.PubAck{}, nil
}

func TestNormalizeAIAgentFeaturesMCPServers(t *testing.T) {
	longTool := strings.Repeat("a", agentMCPMaxAllowedToolChars+20)
	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow: true,
		McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{
			"  b  ": {
				Url:          "  https://b.example.com/mcp  ",
				AllowedTools: []string{" search_docs ", "search_docs", "get_page", "", " a", " b", " c", " d", " e", " f", longTool},
				Headers: map[string]string{
					" Authorization ": " Bearer room-secret ",
					"Host":            "evil.example.com",
					"Content-Length":  "999",
					"X-Empty":         "   ",
				},
			},
			"a":          {Url: "https://a.example.com/mcp"},
			"c":          {Url: "https://c.example.com/mcp"},
			"nil_server": nil,
			"   ":        {Url: "https://blank.example.com/mcp"},
			"no_url":     {Url: "   "},
		},
	}

	out := NormalizeAIAgentFeatures(features)
	servers := out.GetMcpServers()
	if len(servers) != 2 {
		t.Fatalf("servers = %d, want the cap of 2 (%v)", len(servers), servers)
	}
	// The cap is applied to the sorted keys, so a and b win and c is dropped.
	if servers["a"] == nil || servers["b"] == nil {
		t.Fatalf("the cap must keep the sorted keys, got %v", servers)
	}
	b := servers["b"]
	if b.GetUrl() != "https://b.example.com/mcp" {
		t.Errorf("url = %q, want it trimmed", b.GetUrl())
	}
	if got, want := strings.Join(b.GetAllowedTools(), ","), "search_docs,get_page,a,b,c,d,e,f"; got != want {
		t.Errorf("allowed tools = %q, want %q", got, want)
	}
	if len(b.GetAllowedTools()) > agentMCPMaxAllowedTools {
		t.Errorf("allowed tools = %d, want at most %d", len(b.GetAllowedTools()), agentMCPMaxAllowedTools)
	}
	for _, header := range []string{"Host", "Content-Length", "X-Empty"} {
		if _, ok := b.GetHeaders()[header]; ok {
			t.Errorf("header %q must be dropped from the normalized config", header)
		}
	}
	if b.GetHeaders()["Authorization"] != "Bearer room-secret" {
		t.Errorf("headers = %v, want the trimmed authorization header", b.GetHeaders())
	}

	// A clone is returned: the caller's metadata must not be mutated.
	if len(features.GetMcpServers()) != 6 {
		t.Errorf("normalize mutated the source features: %d servers left", len(features.GetMcpServers()))
	}

	empty := NormalizeAIAgentFeatures(&plugnmeet.InsightsAIAgentFeatures{
		McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{"bad": nil},
	})
	if len(empty.GetMcpServers()) != 0 {
		t.Errorf("servers = %v, want none", empty.GetMcpServers())
	}
}

func TestValidateAIAgentMCPServers(t *testing.T) {
	valid := map[string]*plugnmeet.ExternalMCPServerConfig{
		"docs": {Url: "https://mcp.example.com/mcp"},
		"kb":   {Url: "https://8.8.8.8:443/mcp"},
	}
	if err := ValidateExternalMCPServers(valid); err != nil {
		t.Errorf("valid servers were refused: %v", err)
	}

	tooMany := map[string]*plugnmeet.ExternalMCPServerConfig{}
	for _, key := range []string{"a", "b", "c"} {
		tooMany[key] = &plugnmeet.ExternalMCPServerConfig{Url: "https://mcp.example.com/mcp"}
	}

	blocked := map[string]map[string]*plugnmeet.ExternalMCPServerConfig{
		"too many servers": tooMany,
		"http url":         {"docs": {Url: "http://mcp.example.com/mcp"}},
		"other port":       {"docs": {Url: "https://mcp.example.com:8443/mcp"}},
		"credentials":      {"docs": {Url: "https://user:pw@mcp.example.com/mcp"}},
		"fragment":         {"docs": {Url: "https://mcp.example.com/mcp#frag"}},
		"empty url":        {"docs": {Url: "   "}},
		"nil config":       {"docs": nil},
		"empty key":        {"  ": {Url: "https://mcp.example.com/mcp"}},
		"loopback":         {"docs": {Url: "https://127.0.0.1/mcp"}},
		"metadata ip":      {"docs": {Url: "https://169.254.169.254/latest/meta-data"}},
		"private ip":       {"docs": {Url: "https://10.0.0.5/mcp"}},
		"ipv6 loopback":    {"docs": {Url: "https://[::1]/mcp"}},
	}
	for name, servers := range blocked {
		if err := ValidateExternalMCPServers(servers); !errors.Is(err, config.ErrAIAgentInvalidMCPServer) {
			t.Errorf("%s must be refused with ErrAIAgentInvalidMCPServer, got %v", name, err)
		}
	}
}

func TestValidateAIAgentFeaturesRefusesInvalidMCPServers(t *testing.T) {
	model := &AIAgentModel{appConfig: &config.AppConfig{Insights: &config.InsightsConfig{
		Services: map[insights.ServiceType]*config.ServiceConfig{
			insights.ServiceTypeAIAgent: {Provider: config.ProviderOpenAI, ID: "main"},
		},
		Providers: map[config.ProviderType][]config.ProviderAccount{
			config.ProviderOpenAI: {{ID: "main"}},
		},
	}}}

	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:      true,
		SystemPrompt: "You teach fractions.",
		Topic:        "Fractions",
		McpServers:   map[string]*plugnmeet.ExternalMCPServerConfig{"docs": {Url: "https://mcp.example.com/mcp"}},
	}
	if err := model.ValidateAIAgentFeatures(features, nil, true); err != nil {
		t.Fatalf("valid features were refused: %v", err)
	}

	features.McpServers["docs"] = &plugnmeet.ExternalMCPServerConfig{Url: "https://127.0.0.1/mcp"}
	if err := model.ValidateAIAgentFeatures(features, nil, true); !errors.Is(err, config.ErrAIAgentInvalidMCPServer) {
		t.Errorf("a blocked mcp endpoint must be refused, got %v", err)
	}
}

func TestPrepareRoomExternalMCPStripsServers(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	model := newAIAgentTestModel(rc)
	ctx := context.Background()
	roomId := "room-prepare"

	// A previous configuration must not survive a new one.
	for key, value := range map[string]string{
		aiAgentTestKey(roomId, "mcp_config"): "{}",
		aiAgentTestKey(roomId, "mcp_tools"):  "[]",
	} {
		if err := rc.Set(ctx, key, value, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rc.HSet(ctx, aiAgentTestKey(roomId, "mcp_cache"), "field", "value").Err(); err != nil {
		t.Fatal(err)
	}

	maxSession := uint32(600)
	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:           true,
		MaxSessionSeconds: maxSession,
		McpServers: map[string]*plugnmeet.ExternalMCPServerConfig{
			"docs": {
				Url:     "https://127.0.0.1/mcp",
				Headers: map[string]string{"Authorization": "Bearer room-secret"},
			},
		},
	}

	model.PrepareRoomExternalMCP(ctx, roomId, features)

	// The endpoints and their credentials never reach the broadcast metadata.
	if got := features.GetMcpServers(); len(got) != 0 {
		t.Fatalf("mcp servers must be stripped from the room metadata, got %v", got)
	}
	if features.GetMaxSessionSeconds() != 600 {
		t.Errorf("max session seconds = %d, want it untouched", features.GetMaxSessionSeconds())
	}
	// The server is unreachable here, but its configuration must stay stored so
	// the session start can retry the discovery from Redis.
	if exists, err := rc.Exists(ctx, aiAgentTestKey(roomId, "mcp_config")).Result(); err != nil || exists != 1 {
		t.Errorf("the mcp config must survive a failed discovery (err %v)", err)
	}
	for _, key := range []string{
		aiAgentTestKey(roomId, "mcp_tools"),
		aiAgentTestKey(roomId, "mcp_cache"),
	} {
		if exists, err := rc.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Errorf("%s still exists (err %v)", key, err)
		}
	}

	// A room recreated without servers drops the state of the older session.
	model.PrepareRoomExternalMCP(ctx, roomId, &plugnmeet.InsightsAIAgentFeatures{})
	for _, key := range []string{
		aiAgentTestKey(roomId, "mcp_config"),
		aiAgentTestKey(roomId, "mcp_tools"),
		aiAgentTestKey(roomId, "mcp_cache"),
	} {
		if exists, err := rc.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Errorf("%s must be cleared without servers (err %v)", key, err)
		}
	}
}

func TestCleanupRoomStateRemovesExternalMCPKeys(t *testing.T) {
	rc := newAIAgentTestRedis(t)
	model := newAIAgentTestModel(rc)
	ctx := context.Background()
	roomId := "room-cleanup"

	keys := map[string]string{
		aiAgentKey(roomId, aiAgentKeyState):         "{}",
		aiAgentKey(roomId, aiAgentKeyDelegate):      "user-1",
		aiAgentKey(roomId, aiAgentKeyPrevPresenter): "user-2",
		aiAgentTestKey(roomId, "mcp_config"):        "{}",
		aiAgentTestKey(roomId, "mcp_tools"):         "[]",
		aiAgentTestKey(roomId, "kb:doc"):            "doc",
	}
	for key, value := range keys {
		if err := rc.Set(ctx, key, value, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := rc.HSet(ctx, aiAgentTestKey(roomId, "mcp_cache"), "field", "value").Err(); err != nil {
		t.Fatal(err)
	}
	otherRoomKey := aiAgentTestKey("room-keep", "mcp_config")
	if err := rc.Set(ctx, otherRoomKey, "{}", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	model.cleanupRoomState(ctx, roomId)

	for key := range keys {
		if exists, err := rc.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Errorf("%s still exists (err %v)", key, err)
		}
	}
	if exists, err := rc.Exists(ctx, aiAgentTestKey(roomId, "mcp_cache")).Result(); err != nil || exists != 0 {
		t.Errorf("mcp cache still exists (err %v)", err)
	}
	if exists, err := rc.Exists(ctx, otherRoomKey).Result(); err != nil || exists != 1 {
		t.Errorf("another room's key was removed (err %v)", err)
	}
}
