package models

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func ptrTo[T any](v T) *T { return &v }

func TestNormalizeAIAgentCustomProvider(t *testing.T) {
	longModel := strings.Repeat("m", aiAgentCustomProviderMaxModel+20)
	in := &plugnmeet.InsightsAIAgentCustomProvider{
		Provider:       "  OpenAI  ",
		ApiKey:         "  sk-secret  ",
		Endpoint:       ptrTo("  https://gw.example.com/v1  "),
		AuthHeader:     ptrTo("   "),
		RealtimeModel:  ptrTo("  gpt-live-1  "),
		SummarizeModel: ptrTo(longModel),
	}

	out := NormalizeAIAgentCustomProvider(in)
	if out.GetProvider() != "openai" {
		t.Errorf("provider = %q, want it lowercased and trimmed", out.GetProvider())
	}
	if out.GetApiKey() != "sk-secret" {
		t.Errorf("api key = %q, want it trimmed", out.GetApiKey())
	}
	if out.GetEndpoint() != "https://gw.example.com/v1" {
		t.Errorf("endpoint = %q", out.GetEndpoint())
	}
	// an optional field left blank is dropped, never sent empty
	if out.Endpoint == nil || out.AuthHeader != nil {
		t.Errorf("auth header = %v, want nil", out.AuthHeader)
	}
	if out.GetRealtimeModel() != "gpt-live-1" {
		t.Errorf("realtime model = %q", out.GetRealtimeModel())
	}
	if got := len([]rune(out.GetSummarizeModel())); got != aiAgentCustomProviderMaxModel {
		t.Errorf("summarize model length = %d, want the cap of %d", got, aiAgentCustomProviderMaxModel)
	}
	if in.GetProvider() != "  OpenAI  " || in.GetApiKey() != "  sk-secret  " {
		t.Error("the host's request must not be mutated")
	}
	if NormalizeAIAgentCustomProvider(nil) != nil {
		t.Error("a missing provider must stay nil")
	}
}

func TestValidateAIAgentCustomProviderRules(t *testing.T) {
	valid := func() *plugnmeet.InsightsAIAgentCustomProvider {
		return &plugnmeet.InsightsAIAgentCustomProvider{Provider: "openai", ApiKey: "sk-secret"}
	}

	cases := []struct {
		name string
		mut  func(cp *plugnmeet.InsightsAIAgentCustomProvider)
		ok   bool
	}{
		{name: "minimal", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {}, ok: true},
		{name: "google", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Provider = "google" }, ok: true},
		{name: "unknown provider", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Provider = "azure" }},
		{name: "empty key", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.ApiKey = "   " }},
		{name: "key with a trailing newline", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.ApiKey = "sk-secret\n" }, ok: true},
		{name: "key with an injected header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.ApiKey = "sk-secret\r\nX-Evil: 1" }},
		{
			name: "https gateway endpoint",
			mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {
				cp.Endpoint = ptrTo("https://gw.example.com:8443/v1")
			},
			ok: true,
		},
		{name: "http endpoint", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Endpoint = ptrTo("http://gw.example.com/v1") }},
		{name: "endpoint with userinfo", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {
			cp.Endpoint = ptrTo("https://u:p@gw.example.com/v1")
		}},
		{name: "endpoint with query", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {
			cp.Endpoint = ptrTo("https://gw.example.com/v1?key=1")
		}},
		{name: "endpoint with fragment", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Endpoint = ptrTo("https://gw.example.com/v1#x") }},
		{name: "loopback endpoint", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Endpoint = ptrTo("https://127.0.0.1/v1") }},
		{name: "private endpoint", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.Endpoint = ptrTo("https://10.0.0.5/v1") }},
		{name: "endpoint with a bad port", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {
			cp.Endpoint = ptrTo("https://gw.example.com:99999/v1")
		}},
		{name: "custom header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.AuthHeader = ptrTo("x-api-key") }, ok: true},
		{name: "space in header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.AuthHeader = ptrTo("x api key") }},
		{name: "pseudo header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.AuthHeader = ptrTo(":authority") }},
		{name: "forbidden header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.AuthHeader = ptrTo("Cookie") }},
		{name: "hop-by-hop header", mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.AuthHeader = ptrTo("transfer-encoding") }},
		{
			// the provider default fills the delegation model a gpt-live session needs
			name: "gpt-live without a delegation model",
			mut:  func(cp *plugnmeet.InsightsAIAgentCustomProvider) { cp.RealtimeModel = ptrTo("gpt-live-1") },
			ok:   true,
		},
		{
			name: "gpt-live with a delegation model",
			mut: func(cp *plugnmeet.InsightsAIAgentCustomProvider) {
				cp.RealtimeModel = ptrTo("GPT-Live-1")
				cp.DelegationModel = ptrTo("gpt-5.4-mini")
			},
			ok: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp := valid()
			tc.mut(cp)
			err := validateAIAgentCustomProvider(cp)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("the provider must be refused")
			}
		})
	}
}

func TestValidateAIAgentFeaturesAcceptsACustomProviderAlone(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	// a BYOK room needs no ai_agent service in the server config at all
	m.appConfig = &config.AppConfig{Insights: &config.InsightsConfig{}}

	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:      true,
		SystemPrompt: "teach",
		Topic:        "fractions",
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider: "google",
			ApiKey:   "google-key",
		},
	}
	if err := m.ValidateAIAgentFeatures(features, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// without a custom provider the missing server configuration is still fatal
	features.CustomProvider = nil
	if err := m.ValidateAIAgentFeatures(features, nil, true); !errors.Is(err, config.ErrAIAgentProviderMissing) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentProviderMissing)
	}
}

// An existing BYOK room keeps its credentials in Redis only, so starting the
// agent on a server that has no services.ai_agent block must still validate.
func TestValidateRoomAIAgentFeaturesAcceptsStoredCustomProvider(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	// a BYOK room needs no ai_agent service in the server config at all
	m.appConfig = &config.AppConfig{Insights: &config.InsightsConfig{}}
	ctx := context.Background()
	roomId := "room-byok-existing"

	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:      true,
		SystemPrompt: "teach",
		Topic:        "fractions",
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider: "google",
			ApiKey:   "google-key",
		},
	}
	// the room was created through the normal path: the provider is in Redis
	// and the metadata carries none of the credentials anymore
	m.PrepareRoomCustomProvider(ctx, roomId, features)
	if features.GetCustomProvider() != nil {
		t.Fatal("the custom provider must be stripped from the features")
	}

	if err := m.ValidateRoomAIAgentFeatures(ctx, roomId, features, nil, true); err != nil {
		t.Fatalf("an existing byok room must validate without a configured ai_agent service: %v", err)
	}

	// a room without a stored provider still needs the server configuration
	if err := m.ValidateRoomAIAgentFeatures(ctx, "room-without-provider", features, nil, true); !errors.Is(err, config.ErrAIAgentProviderMissing) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentProviderMissing)
	}
	// and the room-less validation of the creation path keeps refusing it too
	if err := m.ValidateAIAgentFeatures(features, nil, true); !errors.Is(err, config.ErrAIAgentProviderMissing) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentProviderMissing)
	}
}

// A breakout room inherits the provider credentials of its parent through the
// Redis clone instead of its metadata, so creating the child must validate
// against the cloned configuration.
func TestValidateBreakoutAIAgentFeaturesUsesClonedProvider(t *testing.T) {
	aiModel := newAIAgentTestModel(newAIAgentTestRedis(t))
	// a byok breakout room needs no ai_agent service in the server config at all
	aiModel.appConfig = &config.AppConfig{Insights: &config.InsightsConfig{}}
	m := &RoomModel{aiAgentModel: aiModel, logger: logrus.NewEntry(logrus.New())}
	ctx := context.Background()

	parentRoomId, childRoomId := "room-parent", "room-parent-1"
	aiModel.PrepareRoomCustomProvider(ctx, parentRoomId, &plugnmeet.InsightsAIAgentFeatures{
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{Provider: "google", ApiKey: "google-key"},
	})
	aiModel.redisService.CopyAIAgentRoomConfig(ctx, parentRoomId, childRoomId, time.Hour)

	newReq := func(roomId string, isBreakout bool) *plugnmeet.CreateRoomReq {
		return &plugnmeet.CreateRoomReq{
			RoomId: roomId,
			Metadata: &plugnmeet.RoomMetadata{
				IsBreakoutRoom: isBreakout,
				RoomFeatures: &plugnmeet.RoomCreateFeatures{
					InsightsFeatures: &plugnmeet.InsightsFeatures{
						IsAllow: true,
						AiAgentFeatures: &plugnmeet.InsightsAIAgentFeatures{
							IsAllow:      true,
							SystemPrompt: "teach",
							Topic:        "fractions",
							SessionMode:  plugnmeet.AIAgentSessionMode_AI_AGENT_SESSION_MODE_CONTROLLED_USERS,
							AutoSessionConfig: &plugnmeet.AIAgentAutoSessionConfig{
								AllowedParticipantIds: []string{"user-1"},
							},
						},
					},
				},
			},
		}
	}

	// the child carries no credentials in its metadata, only the cloned Redis one
	if err := m.validateAIAgentFeatures(ctx, newReq(childRoomId, true), m.logger); err != nil {
		t.Fatalf("a breakout room must validate with its cloned provider: %v", err)
	}

	// a breakout room without a cloned provider still needs the server config
	if err := m.validateAIAgentFeatures(ctx, newReq("room-parent-2", true), m.logger); !errors.Is(err, config.ErrAIAgentProviderMissing) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentProviderMissing)
	}

	// a normal room is validated from its request, never from a stored provider
	if err := m.validateAIAgentFeatures(ctx, newReq(parentRoomId, false), m.logger); !errors.Is(err, config.ErrAIAgentProviderMissing) {
		t.Fatalf("err = %v, want %v", err, config.ErrAIAgentProviderMissing)
	}
}

func TestPrepareRoomCustomProviderStoresAndStrips(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	ctx := context.Background()
	roomId := "room-byok"

	features := &plugnmeet.InsightsAIAgentFeatures{
		IsAllow:           true,
		MaxSessionSeconds: 1800,
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider:   "OpenAI",
			ApiKey:     "sk-secret",
			Endpoint:   ptrTo("https://gw.example.com/v1"),
			AuthHeader: ptrTo("x-api-key"),
		},
	}
	m.PrepareRoomCustomProvider(ctx, roomId, features)

	// credentials must never reach the metadata that is broadcast to everybody
	if features.GetCustomProvider() != nil {
		t.Fatal("the custom provider must be stripped from the features")
	}
	stored, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId)
	if err != nil || stored == nil {
		t.Fatalf("stored = %+v (err %v)", stored, err)
	}
	if stored.Provider != "openai" || stored.APIKey != "sk-secret" {
		t.Errorf("stored = %+v, want the normalized credentials", stored)
	}
	if stored.Endpoint != "https://gw.example.com/v1" || stored.AuthHeader != "x-api-key" {
		t.Errorf("stored endpoint/auth header = %q/%q", stored.Endpoint, stored.AuthHeader)
	}

	account, serviceCfg, isCustom, err := m.resolveAIAgentProvider(ctx, roomId)
	if err != nil || !isCustom {
		t.Fatalf("isCustom = %v (err %v), want true", isCustom, err)
	}
	if account.Credentials.APIKey != "sk-secret" {
		t.Errorf("account key = %q", account.Credentials.APIKey)
	}
	if account.GetOptionsString("endpoint", "") != "https://gw.example.com/v1" {
		t.Errorf("account endpoint = %q", account.GetOptionsString("endpoint", ""))
	}
	if account.GetOptionsString("auth_header", "") != "x-api-key" {
		t.Errorf("account auth header = %q", account.GetOptionsString("auth_header", ""))
	}
	if serviceCfg.Provider != config.ProviderOpenAI {
		t.Errorf("provider = %q, want openai", serviceCfg.Provider)
	}
	if got := serviceCfg.GetOptionsString("realtime_model", ""); got != aiAgentCustomOpenAILiveModel {
		t.Errorf("realtime model = %q, want the openai default", got)
	}
	if got := serviceCfg.GetOptionsString("delegation_model", ""); got != aiAgentCustomOpenAITextModel {
		t.Errorf("delegation model = %q, want the openai default", got)
	}
	if got := serviceCfg.GetOptionsString("realtime_auth_header", ""); got != "x-api-key" {
		t.Errorf("realtime auth header = %q", got)
	}

	// a reused room id created without a provider must not keep the old secrets
	m.PrepareRoomCustomProvider(ctx, roomId, &plugnmeet.InsightsAIAgentFeatures{})
	if stored, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId); err != nil || stored != nil {
		t.Fatalf("stored = %+v (err %v), want it deleted", stored, err)
	}
}

func TestCustomProviderModelOverrides(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	ctx := context.Background()
	roomId := "room-overrides"

	m.PrepareRoomCustomProvider(ctx, roomId, &plugnmeet.InsightsAIAgentFeatures{
		MaxSessionSeconds: 600,
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider:        "google",
			ApiKey:          "google-key",
			RealtimeModel:   ptrTo("gemini-custom-live"),
			DelegationModel: ptrTo("gemini-custom-text"),
			SummarizeModel:  ptrTo("gemini-custom-summary"),
		},
	})

	_, serviceCfg, isCustom, err := m.resolveAIAgentProvider(ctx, roomId)
	if err != nil || !isCustom {
		t.Fatalf("isCustom = %v (err %v)", isCustom, err)
	}
	if serviceCfg.Provider != config.ProviderGoogle {
		t.Errorf("provider = %q, want google", serviceCfg.Provider)
	}
	for key, want := range map[string]string{
		"realtime_model":   "gemini-custom-live",
		"model":            "gemini-custom-live",
		"delegation_model": "gemini-custom-text",
		"summarize_model":  "gemini-custom-summary",
	} {
		if got := serviceCfg.GetOptionsString(key, ""); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// the host route must also drop a server live model, which the provider
	// would otherwise prefer
	if _, ok := serviceCfg.Options["live_model"]; ok {
		t.Error("live_model must not survive a host realtime override")
	}
}

// A gpt-live session delegates every text turn, so the host's realtime override
// must come with the provider's default delegation model.
func TestCustomProviderGPTLiveDelegationDefault(t *testing.T) {
	m := newAIAgentTestModel(newAIAgentTestRedis(t))
	ctx := context.Background()

	m.PrepareRoomCustomProvider(ctx, "room-live", &plugnmeet.InsightsAIAgentFeatures{
		CustomProvider: &plugnmeet.InsightsAIAgentCustomProvider{
			Provider:      "openai",
			ApiKey:        "sk-secret",
			RealtimeModel: ptrTo("gpt-live-1"),
		},
	})

	_, serviceCfg, _, err := m.resolveAIAgentProvider(ctx, "room-live")
	if err != nil {
		t.Fatal(err)
	}
	if got := serviceCfg.GetOptionsString("realtime_model", ""); got != "gpt-live-1" {
		t.Errorf("realtime model = %q, want the host override", got)
	}
	if got := serviceCfg.GetOptionsString("delegation_model", ""); got != aiAgentCustomOpenAITextModel {
		t.Errorf("delegation model = %q, want the openai default", got)
	}
	if got := serviceCfg.GetOptionsString("summarize_model", ""); got != aiAgentCustomOpenAITextModel {
		t.Errorf("summarize model = %q, want the openai default", got)
	}
}

func TestAIAgentCustomProviderTTL(t *testing.T) {
	// an unknown session length is left to the redis service default, which is
	// what a zero duration resolves to
	if got := aiAgentCustomProviderTTL(0); got != 0 {
		t.Errorf("ttl = %s, want 0 to fall back to the default", got)
	}
	if got := aiAgentCustomProviderTTL(1800); got != 90*time.Minute {
		t.Errorf("ttl = %s, want the session plus the buffer of 1h30m", got)
	}
	if got := aiAgentCustomProviderTTL(7200); got != 3*time.Hour {
		t.Errorf("ttl = %s, want 3h", got)
	}
	// a very long session must still be capped to a day
	if got := aiAgentCustomProviderTTL(uint32((48 * time.Hour).Seconds())); got != aiAgentCustomProviderKeyTTL {
		t.Errorf("ttl = %s, want the cap of %s", got, aiAgentCustomProviderKeyTTL)
	}
}
