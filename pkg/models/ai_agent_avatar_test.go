package models

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// newAvatarTestConfig returns a server configuration whose ai_agent service and
// avatar account are fully usable.
func newAvatarTestConfig() *config.AppConfig {
	return &config.AppConfig{Insights: &config.InsightsConfig{
		Enabled: true,
		Providers: map[config.ProviderType][]config.ProviderAccount{
			config.ProviderGoogle: {
				{ID: "gemini", Credentials: config.CredentialsConfig{APIKey: "google-key"}},
			},
			config.ProviderBeyondPresence: {
				{
					ID:          "avatar",
					Credentials: config.CredentialsConfig{APIKey: "avatar-key"},
					Options:     map[string]interface{}{"avatar_id": "server-avatar"},
				},
			},
		},
		Services: map[insights.ServiceType]*config.ServiceConfig{
			insights.ServiceTypeAIAgent: {
				Provider: config.ProviderGoogle,
				ID:       "gemini",
				Options: map[string]interface{}{
					"avatar_provider":   "beyond_presence",
					"avatar_account_id": "avatar",
				},
			},
		},
	}}
}

// avatarFeatures returns an agent feature set with an enabled avatar.
func avatarFeatures(avatar *plugnmeet.InsightsAIAgentAvatarConfig) *plugnmeet.InsightsAIAgentFeatures {
	return &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:      true,
		SystemPrompt: "teach",
		Topic:        "fractions",
		Avatar:       avatar,
	}
}

// An avatar needs the agent's own audio, so a room-managed E2EE key is not
// enough: the server can never read the media.
func TestValidateAIAgentFeaturesRefusesAvatarWithE2EE(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	m.appConfig = newAvatarTestConfig()

	e2ee := &plugnmeet.EndToEndEncryptionFeatures{IsEnabled: true}
	features := avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("beyond_presence"),
		AvatarId:  ptrTo("avatar-1"),
		ApiKey:    ptrTo("avatar-secret"),
	})
	if err := m.ValidateAIAgentFeatures(features, e2ee, true); !errors.Is(err, config.ErrAIAgentE2EEUnsupported) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentE2EEUnsupported)
	}

	// without the avatar the room-managed key stays acceptable
	features.Avatar = nil
	if err := m.ValidateAIAgentFeatures(features, e2ee, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// a self-inserted per-device key is refused on its own: the server can never
	// read the media, with or without an avatar
	selfInsert := &plugnmeet.EndToEndEncryptionFeatures{IsEnabled: true, EnabledSelfInsertEncryptionKey: true}
	if err := m.ValidateAIAgentFeatures(features, selfInsert, true); !errors.Is(err, config.ErrAIAgentE2EEUnsupported) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentE2EEUnsupported)
	}
}

func TestNormalizeAIAgentAvatarConfig(t *testing.T) {
	longID := strings.Repeat("a", aiAgentAvatarMaxID+20)
	out := NormalizeAIAgentAvatarConfig(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled:        true,
		Provider:         ptrTo("  Beyond_Presence  "),
		AvatarId:         ptrTo("  " + longID + "  "),
		MaxAvatarMinutes: ptrTo(uint32(1000)),
		ApiKey:           ptrTo("  avatar-secret  "),
		Endpoint:         ptrTo("  https://avatar-gw.example.com  "),
		AuthHeader:       ptrTo("  x-gateway-key  "),
	})
	if out.GetProvider() != "beyond_presence" {
		t.Errorf("provider = %q, want it lowercased and trimmed", out.GetProvider())
	}
	if got := len([]rune(out.GetAvatarId())); got != aiAgentAvatarMaxID {
		t.Errorf("avatar id length = %d, want the cap of %d", got, aiAgentAvatarMaxID)
	}
	if got := out.GetMaxAvatarMinutes(); got != aiAgentAvatarMaxMinutes {
		t.Errorf("max avatar minutes = %d, want the cap of %d", got, aiAgentAvatarMaxMinutes)
	}
	if out.GetApiKey() != "avatar-secret" {
		t.Errorf("api key = %q, want it trimmed", out.GetApiKey())
	}
	if out.GetEndpoint() != "https://avatar-gw.example.com" {
		t.Errorf("endpoint = %q, want it trimmed", out.GetEndpoint())
	}
	if out.GetAuthHeader() != "x-gateway-key" {
		t.Errorf("auth header = %q, want it trimmed", out.GetAuthHeader())
	}

	// the optional gateway fields are capped like the LLM provider ones
	capped := NormalizeAIAgentAvatarConfig(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled:  true,
		Endpoint:   ptrTo(strings.Repeat("e", aiAgentCustomProviderMaxEndpoint+50)),
		AuthHeader: ptrTo(strings.Repeat("h", aiAgentCustomProviderMaxHeader+50)),
	})
	if got := len([]rune(capped.GetEndpoint())); got != aiAgentCustomProviderMaxEndpoint {
		t.Errorf("endpoint length = %d, want the cap of %d", got, aiAgentCustomProviderMaxEndpoint)
	}
	if got := len([]rune(capped.GetAuthHeader())); got != aiAgentCustomProviderMaxHeader {
		t.Errorf("auth header length = %d, want the cap of %d", got, aiAgentCustomProviderMaxHeader)
	}

	// a disabled or missing avatar config is dropped, never kept around
	if NormalizeAIAgentAvatarConfig(nil) != nil {
		t.Error("a missing avatar must stay nil")
	}
	if out := NormalizeAIAgentAvatarConfig(&plugnmeet.InsightsAIAgentAvatarConfig{AvatarId: ptrTo("avatar-1")}); out != nil {
		t.Errorf("avatar = %+v, want a disabled one dropped", out)
	}
	// optional leftovers are dropped instead of sent empty
	sparse := NormalizeAIAgentAvatarConfig(&plugnmeet.InsightsAIAgentAvatarConfig{IsEnabled: true})
	if sparse.GetProvider() != "" || sparse.AvatarId != nil || sparse.ApiKey != nil ||
		sparse.Endpoint != nil || sparse.AuthHeader != nil {
		t.Errorf("avatar = %+v, want the empty optional fields dropped", sparse)
	}
	blank := NormalizeAIAgentAvatarConfig(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled:  true,
		Endpoint:   ptrTo("   "),
		AuthHeader: ptrTo("   "),
	})
	if blank.Endpoint != nil || blank.AuthHeader != nil {
		t.Errorf("avatar = %+v, want the blank gateway fields dropped", blank)
	}
}

// The avatar key never travels with the metadata: it is stored next to the LLM
// provider, in the same room key, and stripped like any other secret.
func TestPrepareRoomCustomProviderStripsAvatarKey(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	ctx := context.Background()
	roomId := "room-avatar"

	features := &plugnmeet.InsightsAIAgentFeatures{
		MaxSessionSeconds: 1800,
		Avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled:  true,
			Provider:   ptrTo("  Anam  "),
			ApiKey:     ptrTo("  avatar-secret  "),
			Endpoint:   ptrTo("  https://avatar-gw.example.com  "),
			AuthHeader: ptrTo("  x-gateway-key  "),
		},
	}
	m.PrepareRoomCustomProvider(ctx, roomId, features)
	if features.GetAvatar().ApiKey != nil {
		t.Fatal("the avatar key must be stripped from the features")
	}
	if features.GetAvatar().Endpoint != nil || features.GetAvatar().AuthHeader != nil {
		t.Fatal("the avatar gateway overrides must be stripped from the features")
	}
	stored, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId)
	if err != nil || stored == nil {
		t.Fatalf("stored = %+v (err %v)", stored, err)
	}
	if stored.AvatarAPIKey != "avatar-secret" {
		t.Errorf("stored avatar key = %q, want the trimmed key", stored.AvatarAPIKey)
	}
	if stored.AvatarProvider != "anam" {
		t.Errorf("stored avatar provider = %q, want it lowercased and trimmed", stored.AvatarProvider)
	}
	if stored.AvatarEndpoint != "https://avatar-gw.example.com" {
		t.Errorf("stored avatar endpoint = %q, want the trimmed one", stored.AvatarEndpoint)
	}
	if stored.AvatarAuthHeader != "x-gateway-key" {
		t.Errorf("stored avatar auth header = %q, want the trimmed one", stored.AvatarAuthHeader)
	}
	// an avatar-only room stores no LLM provider
	if stored.Provider != "" || stored.APIKey != "" {
		t.Errorf("stored provider = %q/%q, want none", stored.Provider, stored.APIKey)
	}
	storedAvatar := m.loadRoomCustomAvatarConfig(ctx, roomId)
	if storedAvatar.Provider != "anam" || storedAvatar.APIKey != "avatar-secret" {
		t.Errorf("stored avatar = %+v, want the stored provider and key", storedAvatar)
	}
	if storedAvatar.Endpoint != "https://avatar-gw.example.com" || storedAvatar.AuthHeader != "x-gateway-key" {
		t.Errorf("stored avatar = %+v, want the stored gateway endpoint and header", storedAvatar)
	}
	if m.loadRoomCustomProvider(ctx, roomId) != nil {
		t.Error("an avatar key alone must not count as an LLM provider")
	}

	// the LLM provider and the avatar key share the room key
	m.PrepareRoomCustomProvider(ctx, roomId, &plugnmeet.InsightsAIAgentFeatures{
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{Provider: "google", ApiKey: "google-key"},
		Avatar:         &plugnmeet.InsightsAIAgentAvatarConfig{IsEnabled: true, ApiKey: ptrTo("avatar-secret")},
	})
	stored, err = m.redisService.LoadAIAgentCustomProvider(ctx, roomId)
	if err != nil || stored == nil {
		t.Fatalf("stored = %+v (err %v)", stored, err)
	}
	if stored.APIKey != "google-key" || stored.AvatarAPIKey != "avatar-secret" {
		t.Errorf("stored = %+v, want both keys", stored)
	}

	// an avatar gateway override alone is enough to keep the configuration
	m.PrepareRoomCustomProvider(ctx, "room-avatar-endpoint-only", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Endpoint:  ptrTo("https://avatar-gw.example.com"),
	}))
	if stored, err := m.redisService.LoadAIAgentCustomProvider(ctx, "room-avatar-endpoint-only"); err != nil ||
		stored == nil || stored.AvatarEndpoint != "https://avatar-gw.example.com" {
		t.Fatalf("stored = %+v (err %v), want the avatar endpoint kept", stored, err)
	}

	// a reused room id created without any secret drops both
	m.PrepareRoomCustomProvider(ctx, roomId, &plugnmeet.InsightsAIAgentFeatures{})
	if stored, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId); err != nil || stored != nil {
		t.Fatalf("stored = %+v (err %v), want it deleted", stored, err)
	}
}

func TestResolveAIAgentAvatarConfig(t *testing.T) {
	ctx := context.Background()
	newModel := func() *AIAgentModel {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		m.appConfig = newAvatarTestConfig()
		return m
	}
	byok := &plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled:  true,
		Provider:   ptrTo("Anam"),
		AvatarId:   ptrTo(" avatar-1 "),
		ApiKey:     ptrTo(" avatar-secret "),
		Endpoint:   ptrTo(" https://avatar-gw.example.com "),
		AuthHeader: ptrTo(" x-gateway-key "),
	}

	m := newModel()
	provider, avatarID, apiKey, endpoint, authHeader, isCustom, err := m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(byok))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != config.ProviderAnam || avatarID != "avatar-1" || apiKey != "avatar-secret" ||
		endpoint != "https://avatar-gw.example.com" || authHeader != "x-gateway-key" || !isCustom {
		t.Errorf("resolved = %q/%q/%q/%q/%q custom=%v, want the room's own avatar", provider, avatarID, apiKey, endpoint, authHeader, isCustom)
	}

	// a stored key answers for an already created room, whose metadata is stripped
	roomId := "room-avatar-start"
	m.PrepareRoomCustomProvider(ctx, roomId, avatarFeatures(byok))
	stripped := avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("anam"),
		AvatarId:  ptrTo("avatar-1"),
	})
	if err := m.ValidateRoomAIAgentFeatures(ctx, roomId, stripped, nil, true); err != nil {
		t.Fatalf("a stored avatar key must validate at start time: %v", err)
	}
	if _, _, apiKey, endpoint, authHeader, isCustom, err = m.resolveAIAgentAvatarConfig(ctx, roomId, stripped); err != nil ||
		apiKey != "avatar-secret" || endpoint != "https://avatar-gw.example.com" || authHeader != "x-gateway-key" || !isCustom {
		t.Errorf("resolved key=%q endpoint=%q header=%q custom=%v (err %v), want the stored ones", apiKey, endpoint, authHeader, isCustom, err)
	}
	// the stored provider answers for a request that carries none
	storedProviderFeatures := avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		AvatarId:  ptrTo("avatar-1"),
	})
	if provider, _, apiKey, _, _, isCustom, err = m.resolveAIAgentAvatarConfig(ctx, roomId, storedProviderFeatures); err != nil {
		t.Fatalf("a stored avatar provider must be honoured: %v", err)
	}
	if provider != config.ProviderAnam || apiKey != "avatar-secret" || !isCustom {
		t.Errorf("resolved = %q/%q custom=%v, want the stored provider and key", provider, apiKey, isCustom)
	}
	// a room without a stored key falls back to nothing
	if _, _, _, _, _, _, err = m.resolveAIAgentAvatarConfig(ctx, "room-no-key", stripped); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
	}

	// everything may come from the server configuration
	_, _, _, _, _, isCustom, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("beyond_presence"),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isCustom {
		t.Error("a server-configured avatar key is not a room key")
	}
	provider, avatarID, apiKey, endpoint, authHeader, _, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{IsEnabled: true}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider != config.ProviderBeyondPresence || avatarID != "server-avatar" || apiKey != "avatar-key" {
		t.Errorf("resolved = %q/%q/%q, want the server configuration", provider, avatarID, apiKey)
	}
	if endpoint != "" || authHeader != "" {
		t.Errorf("endpoint = %q header = %q, want the account to leave them unset", endpoint, authHeader)
	}

	// an unsupported provider is refused, even when it is configured
	m.appConfig.Insights.Providers[config.ProviderSimli] = []config.ProviderAccount{
		{ID: "avatar", Credentials: config.CredentialsConfig{APIKey: "avatar-key"}},
	}
	if provider, _, _, _, _, _, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("simli"),
		AvatarId:  ptrTo("avatar-1"),
	})); err != nil {
		t.Fatalf("simli must be accepted: %v", err)
	}
	if provider != config.ProviderSimli {
		t.Errorf("provider = %q, want simli", provider)
	}
	if _, _, _, _, _, _, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("unknown"),
	})); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
	}
	// a key with a trailing newline could inject a header
	if _, _, _, _, _, _, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
		IsEnabled: true,
		Provider:  ptrTo("beyond_presence"),
		AvatarId:  ptrTo("avatar-1"),
		ApiKey:    ptrTo("avatar\r\nX-Evil: 1"),
	})); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
	}
	// a disabled avatar has nothing to resolve
	if _, _, _, _, _, _, err = m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(nil)); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
	}
}

// The gateway endpoint and the header carrying the key follow the same
// fallback order as the rest of the configuration and are validated wherever
// they come from.
func TestResolveAIAgentAvatarGatewayOverrides(t *testing.T) {
	ctx := context.Background()

	t.Run("request endpoint and header win", func(t *testing.T) {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		m.appConfig = newAvatarTestConfig()
		_, _, _, endpoint, authHeader, _, err := m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled:  true,
			Provider:   ptrTo("beyond_presence"),
			AvatarId:   ptrTo("avatar-1"),
			ApiKey:     ptrTo("avatar-secret"),
			Endpoint:   ptrTo("https://avatar-gw.example.com"),
			AuthHeader: ptrTo("x-gateway-key"),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != "https://avatar-gw.example.com" || authHeader != "x-gateway-key" {
			t.Errorf("endpoint = %q header = %q, want the request overrides", endpoint, authHeader)
		}
	})

	t.Run("stored endpoint and header answer for a stripped room", func(t *testing.T) {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		m.appConfig = newAvatarTestConfig()
		roomId := "room-avatar-gateway"
		m.PrepareRoomCustomProvider(ctx, roomId, avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled:  true,
			Provider:   ptrTo("beyond_presence"),
			AvatarId:   ptrTo("avatar-1"),
			ApiKey:     ptrTo("avatar-secret"),
			Endpoint:   ptrTo("https://avatar-gw.example.com"),
			AuthHeader: ptrTo("x-gateway-key"),
		}))
		_, _, _, endpoint, authHeader, _, err := m.resolveAIAgentAvatarConfig(ctx, roomId, avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled: true,
			AvatarId:  ptrTo("avatar-1"),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != "https://avatar-gw.example.com" || authHeader != "x-gateway-key" {
			t.Errorf("endpoint = %q header = %q, want the stored ones", endpoint, authHeader)
		}
	})

	t.Run("server account is the last resort", func(t *testing.T) {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		cfg := newAvatarTestConfig()
		cfg.Insights.Providers[config.ProviderBeyondPresence] = []config.ProviderAccount{
			{
				ID:          "avatar",
				Credentials: config.CredentialsConfig{APIKey: "avatar-key"},
				Options: map[string]interface{}{
					"avatar_id":   "server-avatar",
					"endpoint":    "https://account-gw.example.com",
					"auth_header": "Authorization",
				},
			},
		}
		m.appConfig = cfg
		_, _, _, endpoint, authHeader, _, err := m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled: true,
			Provider:  ptrTo("beyond_presence"),
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if endpoint != "https://account-gw.example.com" || authHeader != "Authorization" {
			t.Errorf("endpoint = %q header = %q, want the account ones", endpoint, authHeader)
		}
	})

	t.Run("unsafe endpoint and header are refused", func(t *testing.T) {
		tests := []struct {
			name   string
			avatar *plugnmeet.InsightsAIAgentAvatarConfig
		}{
			{
				name: "plain http endpoint",
				avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
					IsEnabled: true,
					Provider:  ptrTo("beyond_presence"),
					AvatarId:  ptrTo("avatar-1"),
					ApiKey:    ptrTo("avatar-secret"),
					Endpoint:  ptrTo("http://avatar-gw.example.com"),
				},
			},
			{
				name: "internal ip endpoint",
				avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
					IsEnabled: true,
					Provider:  ptrTo("beyond_presence"),
					AvatarId:  ptrTo("avatar-1"),
					ApiKey:    ptrTo("avatar-secret"),
					Endpoint:  ptrTo("https://10.0.0.5"),
				},
			},
			{
				name: "endpoint with credentials",
				avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
					IsEnabled: true,
					Provider:  ptrTo("beyond_presence"),
					AvatarId:  ptrTo("avatar-1"),
					ApiKey:    ptrTo("avatar-secret"),
					Endpoint:  ptrTo("https://user:pass@avatar-gw.example.com"),
				},
			},
			{
				name: "host header",
				avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
					IsEnabled:  true,
					Provider:   ptrTo("beyond_presence"),
					AvatarId:   ptrTo("avatar-1"),
					ApiKey:     ptrTo("avatar-secret"),
					AuthHeader: ptrTo("Host"),
				},
			},
			{
				name: "header with a colon",
				avatar: &plugnmeet.InsightsAIAgentAvatarConfig{
					IsEnabled:  true,
					Provider:   ptrTo("beyond_presence"),
					AvatarId:   ptrTo("avatar-1"),
					ApiKey:     ptrTo("avatar-secret"),
					AuthHeader: ptrTo("X-Evil: 1"),
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				m := newAIAgentTestModel(newAIAgentTestRedis(t))
				m.appConfig = newAvatarTestConfig()
				if _, _, _, _, _, _, err := m.resolveAIAgentAvatarConfig(ctx, "", avatarFeatures(tt.avatar)); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
					t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
				}
			})
		}
	})

	t.Run("unsafe stored values are refused too", func(t *testing.T) {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		m.appConfig = newAvatarTestConfig()
		roomId := "room-avatar-stored-unsafe"
		// the stored configuration is written directly: it can predate a rule
		if err := m.redisService.SaveAIAgentCustomProvider(ctx, roomId, redisservice.AIAgentCustomProviderConfig{
			AvatarProvider:   "beyond_presence",
			AvatarAPIKey:     "avatar-secret",
			AvatarEndpoint:   "https://10.0.0.5",
			AvatarAuthHeader: "Host",
		}, time.Hour); err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, _, _, err := m.resolveAIAgentAvatarConfig(ctx, roomId, avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled: true,
			AvatarId:  ptrTo("avatar-1"),
		})); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
			t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
		}
	})
}

func TestValidateAIAgentFeaturesAvatarRules(t *testing.T) {
	newModel := func(insightsCfg *config.InsightsConfig) *AIAgentModel {
		m := newAIAgentTestModel(newAIAgentTestRedis(t))
		m.appConfig = &config.AppConfig{Insights: insightsCfg}
		return m
	}

	valid := func() *plugnmeet.InsightsAIAgentAvatarConfig {
		return &plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled: true,
			Provider:  ptrTo("beyond_presence"),
			AvatarId:  ptrTo("avatar-1"),
			ApiKey:    ptrTo("avatar-secret"),
		}
	}

	t.Run("valid byok avatar", func(t *testing.T) {
		// the room brings both the LLM provider and the avatar key
		m := newModel(nil)
		features := avatarFeatures(valid())
		features.CustomProvider = &plugnmeet.InsightsAIAgentCustomProvider{Provider: "google", ApiKey: "google-key"}
		if err := m.ValidateAIAgentFeatures(features, nil, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("valid server configured avatar", func(t *testing.T) {
		m := newModel(newAvatarTestConfig().Insights)
		// nothing but the switch: provider, avatar id and key all come from config
		if err := m.ValidateAIAgentFeatures(avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{IsEnabled: true}), nil, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		m := newModel(newAvatarTestConfig().Insights)
		avatar := valid()
		avatar.Provider = ptrTo("heygen")
		if err := m.ValidateAIAgentFeatures(avatarFeatures(avatar), nil, true); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
			t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
		}
	})

	t.Run("missing avatar id", func(t *testing.T) {
		m := newModel(newAvatarTestConfig().Insights)
		avatar := valid()
		avatar.AvatarId = nil
		// the server account of beyond_presence carries an avatar id, so the
		// provider itself has to be unconfigured too
		m.appConfig.Insights.Providers[config.ProviderBeyondPresence] = []config.ProviderAccount{
			{ID: "avatar", Credentials: config.CredentialsConfig{APIKey: "avatar-key"}},
		}
		if err := m.ValidateAIAgentFeatures(avatarFeatures(avatar), nil, true); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
			t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
		}
	})

	t.Run("missing api key", func(t *testing.T) {
		m := newModel(newAvatarTestConfig().Insights)
		m.appConfig.Insights.Providers[config.ProviderBeyondPresence] = []config.ProviderAccount{
			{ID: "avatar", Options: map[string]interface{}{"avatar_id": "server-avatar"}},
		}
		avatar := valid()
		avatar.ApiKey = nil
		if err := m.ValidateAIAgentFeatures(avatarFeatures(avatar), nil, true); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
			t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
		}
	})

	t.Run("no avatar account and no key", func(t *testing.T) {
		m := newModel(newAvatarTestConfig().Insights)
		delete(m.appConfig.Insights.Providers, config.ProviderBeyondPresence)
		if err := m.ValidateAIAgentFeatures(avatarFeatures(&plugnmeet.InsightsAIAgentAvatarConfig{
			IsEnabled: true,
			Provider:  ptrTo("beyond_presence"),
			AvatarId:  ptrTo("avatar-1"),
		}), nil, true); !errors.Is(err, config.ErrAIAgentInvalidRequest) {
			t.Fatalf("err = %v, want %v", err, config.ErrAIAgentInvalidRequest)
		}
	})
}
