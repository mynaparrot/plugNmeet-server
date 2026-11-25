package redisservice

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

func TestAIAgentCustomProviderRoundTrip(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	if stored, err := s.LoadAIAgentCustomProvider(ctx, "room-a"); err != nil || stored != nil {
		t.Fatalf("a room without a provider must load nothing (err %v, stored %+v)", err, stored)
	}

	cfg := AIAgentCustomProviderConfig{
		Provider:        "openai",
		APIKey:          "sk-secret",
		Endpoint:        "https://gw.example.com/v1",
		AuthHeader:      "x-api-key",
		RealtimeModel:   "gpt-realtime-1.5",
		DelegationModel: "gpt-5.4-mini",
		SummarizeModel:  "gpt-5.4-mini",
	}
	if err := s.SaveAIAgentCustomProvider(ctx, "room-a", cfg, ttl); err != nil {
		t.Fatal(err)
	}
	stored, err := s.LoadAIAgentCustomProvider(ctx, "room-a")
	if err != nil || stored == nil || *stored != cfg {
		t.Fatalf("stored = %+v (err %v), want %+v", stored, err, cfg)
	}
	if left, err := rc.TTL(ctx, aiAgentProviderConfigKey("room-a")).Result(); err != nil || left > ttl || left <= 0 {
		t.Errorf("ttl = %s (err %v), want at most %s", left, err, ttl)
	}

	if err := s.DeleteAIAgentCustomProvider(ctx, "room-a"); err != nil {
		t.Fatal(err)
	}
	if stored, err := s.LoadAIAgentCustomProvider(ctx, "room-a"); err != nil || stored != nil {
		t.Fatalf("stored = %+v (err %v), want it deleted", stored, err)
	}
}

func TestAIAgentCustomProviderAvatarKeyRoundTrip(t *testing.T) {
	s, _ := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	// an avatar-only room (server-configured LLM provider) still needs its
	// provider, key, gateway endpoint and auth header
	avatarOnly := AIAgentCustomProviderConfig{
		AvatarProvider:   "anam",
		AvatarAPIKey:     "avatar-secret",
		AvatarEndpoint:   "https://avatar-gw.example.com",
		AvatarAuthHeader: "x-avatar-key",
	}
	if err := s.SaveAIAgentCustomProvider(ctx, "room-avatar", avatarOnly, ttl); err != nil {
		t.Fatal(err)
	}
	stored, err := s.LoadAIAgentCustomProvider(ctx, "room-avatar")
	if err != nil || stored == nil || *stored != avatarOnly {
		t.Fatalf("stored = %+v (err %v), want %+v", stored, err, avatarOnly)
	}
	if stored.AvatarProvider != "anam" {
		t.Errorf("avatar provider = %q, want the stored one", stored.AvatarProvider)
	}
	if stored.AvatarEndpoint != "https://avatar-gw.example.com" {
		t.Errorf("avatar endpoint = %q, want the stored one", stored.AvatarEndpoint)
	}
	if stored.AvatarAuthHeader != "x-avatar-key" {
		t.Errorf("avatar auth header = %q, want the stored one", stored.AvatarAuthHeader)
	}

	// the avatar provider, key, endpoint and header travel alongside a BYOK
	// provider in the same room key
	both := AIAgentCustomProviderConfig{
		Provider:         "openai",
		APIKey:           "sk-secret",
		AvatarProvider:   "beyond_presence",
		AvatarAPIKey:     "avatar-secret",
		AvatarEndpoint:   "https://avatar-gw.example.com",
		AvatarAuthHeader: "Authorization",
	}
	if err := s.SaveAIAgentCustomProvider(ctx, "room-both", both, ttl); err != nil {
		t.Fatal(err)
	}
	stored, err = s.LoadAIAgentCustomProvider(ctx, "room-both")
	if err != nil || stored == nil || *stored != both {
		t.Fatalf("stored = %+v (err %v), want %+v", stored, err, both)
	}

	// fields the room does not set are omitted, never sent empty
	encoded, err := json.Marshal(AIAgentCustomProviderConfig{APIKey: "sk-secret"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"avatar_api_key", "avatar_provider", "avatar_endpoint", "avatar_auth_header"} {
		if strings.Contains(string(encoded), field) {
			t.Errorf("encoded = %s, want %q omitted", encoded, field)
		}
	}

	// the 1-on-1 child room inherits it with the rest of the configuration
	s.CopyAIAgentRoomConfig(ctx, "room-both", "room-both-1", ttl)
	child, err := s.LoadAIAgentCustomProvider(ctx, "room-both-1")
	if err != nil || child == nil || *child != both {
		t.Fatalf("child = %+v (err %v), want %+v", child, err, both)
	}
}

func TestAIAgentProviderTTLFallsBackToDefault(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	want := s.defaultTTL // the TTL the test service is built with

	ttlOf := func(key string) time.Duration {
		t.Helper()
		left, err := rc.TTL(ctx, key).Result()
		if err != nil {
			t.Fatalf("ttl of %q: %v", key, err)
		}
		return left
	}

	// An unset ttl must not leave a permanent key behind: it falls back to the
	// service's default, like every other key of the service.
	cases := []struct {
		roomId string
		ttl    time.Duration
	}{
		{roomId: "room-zero-ttl", ttl: 0},
		{roomId: "room-negative-ttl", ttl: -time.Minute},
	}
	cfg := AIAgentCustomProviderConfig{Provider: "openai", APIKey: "sk-secret"}
	for _, c := range cases {
		if err := s.SaveAIAgentCustomProvider(ctx, c.roomId, cfg, c.ttl); err != nil {
			t.Fatalf("ttl %s: %v", c.ttl, err)
		}
		if left := ttlOf(aiAgentProviderConfigKey(c.roomId)); left <= 0 || left > want {
			t.Errorf("ttl %s: stored ttl = %s, want the default of %s", c.ttl, left, want)
		}
	}

	// Copying the config to a breakout room resolves the ttl the same way.
	if err := s.SaveAIAgentCustomProvider(ctx, "parent-default-ttl", cfg, time.Minute); err != nil {
		t.Fatal(err)
	}
	s.CopyAIAgentRoomConfig(ctx, "parent-default-ttl", "child-default-ttl", 0)
	if child, err := s.LoadAIAgentCustomProvider(ctx, "child-default-ttl"); err != nil || child == nil || *child != cfg {
		t.Fatalf("child provider = %+v (err %v), want %+v", child, err, cfg)
	}
	if left := ttlOf(aiAgentProviderConfigKey("child-default-ttl")); left <= 0 || left > want {
		t.Errorf("copied ttl = %s, want the default of %s", left, want)
	}
}

func TestCopyAIAgentRoomConfig(t *testing.T) {
	s, rc := newMCPTestService(t)
	ctx := context.Background()
	ttl := time.Hour

	// nothing to copy: the child stays empty
	s.CopyAIAgentRoomConfig(ctx, "parent", "child", ttl)
	for _, key := range aiAgentRoomConfigKeys("child") {
		if exists, err := rc.Exists(ctx, key).Result(); err != nil || exists != 0 {
			t.Fatalf("key %q exists = %d (err %v), want nothing", key, exists, err)
		}
	}

	// a room id never clones onto itself
	if err := s.SaveAIAgentCustomProvider(ctx, "self", AIAgentCustomProviderConfig{Provider: "google", APIKey: "k"}, ttl); err != nil {
		t.Fatal(err)
	}
	s.CopyAIAgentRoomConfig(ctx, "self", "self", ttl)
	if left, err := rc.TTL(ctx, aiAgentProviderConfigKey("self")).Result(); err != nil || left != ttl {
		t.Fatalf("ttl = %s (err %v), want it untouched at %s", left, err, ttl)
	}

	cfg := AIAgentCustomProviderConfig{Provider: "google", APIKey: "google-key"}
	if err := s.SaveAIAgentCustomProvider(ctx, "parent", cfg, ttl); err != nil {
		t.Fatal(err)
	}
	servers := map[string]*plugnmeet.ExternalMCPServerConfig{
		"docs": {Url: "https://mcp.example.com/mcp"},
	}
	if err := s.SaveExternalMCPConfig(ctx, "parent", servers, ttl); err != nil {
		t.Fatal(err)
	}
	tools := []*insights.CachedExternalMCPTool{
		{ServerKey: "docs", ServerSlug: "docs", RemoteToolName: "search"},
	}
	if err := s.SaveExternalMCPTools(ctx, "parent", tools, ttl); err != nil {
		t.Fatal(err)
	}

	s.CopyAIAgentRoomConfig(ctx, "parent", "child", ttl)
	child, err := s.LoadAIAgentCustomProvider(ctx, "child")
	if err != nil || child == nil || *child != cfg {
		t.Fatalf("child provider = %+v (err %v), want %+v", child, err, cfg)
	}
	if childServers, err := s.LoadExternalMCPConfig(ctx, "child"); err != nil || len(childServers) != 1 {
		t.Fatalf("child servers = %+v (err %v)", childServers, err)
	}
	childTools, err := s.LoadExternalMCPTools(ctx, "child")
	if err != nil || len(childTools) != 1 || childTools[0].RemoteToolName != "search" {
		t.Fatalf("child tools = %+v (err %v)", childTools, err)
	}
	// the clone keeps the parent's TTL and is written verbatim
	for _, key := range aiAgentRoomConfigKeys("child") {
		if left, err := rc.TTL(ctx, key).Result(); err != nil || left > ttl || left <= 0 {
			t.Errorf("ttl of %q = %s (err %v), want at most %s", key, left, err, ttl)
		}
	}
	// a parent that only has some of the keys clones only those
	if err := s.DeleteAIAgentCustomProvider(ctx, "parent"); err != nil {
		t.Fatal(err)
	}
	s.CopyAIAgentRoomConfig(ctx, "parent", "child-2", ttl)
	if stored, err := s.LoadAIAgentCustomProvider(ctx, "child-2"); err != nil || stored != nil {
		t.Fatalf("provider = %+v (err %v), want nothing", stored, err)
	}
	if childServers, err := s.LoadExternalMCPConfig(ctx, "child-2"); err != nil || len(childServers) != 1 {
		t.Fatalf("servers = %+v (err %v), want the parent's servers", childServers, err)
	}
}
