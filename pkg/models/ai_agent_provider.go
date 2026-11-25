package models

import (
	"context"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// aiAgentCustomProviderKeyTTL bounds how long the room's provider credentials
// outlive the room, mirroring the external MCP keys.
const aiAgentCustomProviderKeyTTL = 24 * time.Hour

// aiAgentCustomProviderTTL derives the TTL of the room's provider
// configuration: long enough for the whole session, never longer than a day.
// Without a known session length it returns 0, which leaves the TTL to
// RedisService's default instead of pinning a guess.
func aiAgentCustomProviderTTL(maxSessionSeconds uint32) time.Duration {
	if maxSessionSeconds == 0 {
		return 0
	}

	return min(time.Duration(maxSessionSeconds)*time.Second+time.Hour, aiAgentCustomProviderKeyTTL)
}

// PrepareRoomCustomProvider moves the host-supplied provider credentials out of
// the room metadata, which is broadcast to every participant, and keeps them in
// server-side Redis only. It must run after the features are validated and
// before the metadata is published. A room recreated without a custom provider
// drops the credentials of an older session that used the same room id.
func (m *AIAgentModel) PrepareRoomCustomProvider(ctx context.Context, roomId string, agentFeatures *plugnmeet.InsightsAIAgentFeatures) {
	if agentFeatures == nil {
		return
	}

	custom := NormalizeAIAgentCustomProvider(agentFeatures.GetCustomProvider())
	avatar := agentFeatures.GetAvatar()
	var avatarProvider, avatarAPIKey, avatarEndpoint, avatarAuthHeader string
	if avatar != nil {
		avatarProvider = strings.ToLower(strings.TrimSpace(avatar.GetProvider()))
		avatarAPIKey = strings.TrimSpace(avatar.GetApiKey())
		avatarEndpoint = strings.TrimSpace(avatar.GetEndpoint())
		avatarAuthHeader = strings.TrimSpace(avatar.GetAuthHeader())
	}
	// Credentials and gateway overrides never travel with the metadata, so they
	// cannot leak through NATS to the browsers; they are only accepted while
	// creating the room.
	agentFeatures.CustomProvider = nil
	if agentFeatures.Avatar != nil {
		agentFeatures.Avatar.ApiKey = nil
		agentFeatures.Avatar.Endpoint = nil
		agentFeatures.Avatar.AuthHeader = nil
	}

	if custom == nil && avatarAPIKey == "" && avatarProvider == "" && avatarEndpoint == "" && avatarAuthHeader == "" {
		if err := m.redisService.DeleteAIAgentCustomProvider(ctx, roomId); err != nil {
			m.logger.WithError(err).WithField("room_id", roomId).Debug("the stale custom ai provider configuration could not be removed")
		}
		return
	}

	cfg := redisservice.AIAgentCustomProviderConfig{
		AvatarProvider:   avatarProvider,
		AvatarAPIKey:     avatarAPIKey,
		AvatarEndpoint:   avatarEndpoint,
		AvatarAuthHeader: avatarAuthHeader,
	}
	if custom != nil {
		cfg.Provider = custom.GetProvider()
		cfg.APIKey = custom.GetApiKey()
		cfg.Endpoint = custom.GetEndpoint()
		cfg.AuthHeader = custom.GetAuthHeader()
		cfg.RealtimeModel = custom.GetRealtimeModel()
		cfg.DelegationModel = custom.GetDelegationModel()
		cfg.SummarizeModel = custom.GetSummarizeModel()
	}

	err := m.redisService.SaveAIAgentCustomProvider(ctx, roomId, cfg, aiAgentCustomProviderTTL(agentFeatures.GetMaxSessionSeconds()))
	if err != nil {
		m.logger.WithError(err).WithField("room_id", roomId).Warn("the custom ai provider configuration could not be stored")
	}
}

// ValidateRoomAIAgentFeatures validates the agent features of a room that
// already exists, for instance when the agent is started manually. The room's
// provider credentials are stripped from the metadata at creation time and kept
// in Redis only, so a stored provider stands in for the server-configured
// ai_agent service; without one the service is still required.
func (m *AIAgentModel) ValidateRoomAIAgentFeatures(ctx context.Context, roomId string, f *plugnmeet.InsightsAIAgentFeatures, e2ee *plugnmeet.EndToEndEncryptionFeatures, insightsAllowed bool) error {
	return m.validateAIAgentFeatures(ctx, roomId, f, e2ee, insightsAllowed, m.loadRoomCustomProvider(ctx, roomId) != nil)
}

// resolveAIAgentProvider resolves the provider of a room: the host's own
// credentials when the room was created with them, otherwise the server
// configuration. The third return value reports which of the two is used.
func (m *AIAgentModel) resolveAIAgentProvider(ctx context.Context, roomId string) (*config.ProviderAccount, *config.ServiceConfig, bool, error) {
	if custom := m.loadRoomCustomProvider(ctx, roomId); custom != nil {
		account, serviceCfg := m.buildCustomProviderConfig(custom)
		return account, serviceCfg, true, nil
	}

	if m.appConfig.Insights == nil {
		return nil, nil, false, config.ErrAIAgentProviderMissing
	}
	account, serviceCfg, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)

	return account, serviceCfg, false, err
}

// loadRoomCustomProvider returns the room's own provider, or the one of its
// parent room when the room is a 1-on-1 breakout session.
func (m *AIAgentModel) loadRoomCustomProvider(ctx context.Context, roomId string) *redisservice.AIAgentCustomProviderConfig {
	if roomId == "" {
		return nil
	}

	custom, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId)
	if err != nil {
		m.logger.WithError(err).WithField("room_id", roomId).Warn("could not read the custom ai provider configuration")
	}
	if custom != nil && strings.TrimSpace(custom.APIKey) != "" {
		return custom
	}

	// The configuration is normally copied when the child room is created; this
	// is the safety net for a 1-on-1 session room that predates the copy.
	parentRoomId := m.parentRoomID(roomId)
	if parentRoomId == "" {
		return nil
	}
	parent, err := m.redisService.LoadAIAgentCustomProvider(ctx, parentRoomId)
	if err != nil || parent == nil || strings.TrimSpace(parent.APIKey) == "" {
		return nil
	}

	return parent
}

// aiAgentStoredAvatarConfig is the avatar part of a room's stored provider
// configuration: the provider, its key, and the gateway endpoint and header the
// key travels in when the room brought its own.
type aiAgentStoredAvatarConfig struct {
	Provider   string
	APIKey     string
	Endpoint   string
	AuthHeader string
}

// isEmpty reports that the room stored no avatar configuration of its own.
func (c aiAgentStoredAvatarConfig) isEmpty() bool {
	return c.Provider == "" && c.APIKey == "" && c.Endpoint == "" && c.AuthHeader == ""
}

// storedAvatarConfig returns the normalized avatar part of a stored provider
// configuration.
func storedAvatarConfig(custom *redisservice.AIAgentCustomProviderConfig) aiAgentStoredAvatarConfig {
	if custom == nil {
		return aiAgentStoredAvatarConfig{}
	}

	return aiAgentStoredAvatarConfig{
		Provider:   strings.ToLower(strings.TrimSpace(custom.AvatarProvider)),
		APIKey:     strings.TrimSpace(custom.AvatarAPIKey),
		Endpoint:   strings.TrimSpace(custom.AvatarEndpoint),
		AuthHeader: strings.TrimSpace(custom.AvatarAuthHeader),
	}
}

// loadRoomCustomAvatarConfig returns the avatar configuration the room brought
// itself, or the one of its parent room when the room is a 1-on-1 breakout
// session. It is empty when the room has no own avatar.
func (m *AIAgentModel) loadRoomCustomAvatarConfig(ctx context.Context, roomId string) aiAgentStoredAvatarConfig {
	if roomId == "" {
		return aiAgentStoredAvatarConfig{}
	}

	// a room may store the avatar configuration without an LLM provider, so it
	// is read independently of the provider one
	if custom := storedAvatarConfig(m.loadRoomCustomAvatarConfigFor(ctx, roomId)); !custom.isEmpty() {
		return custom
	}

	parentRoomId := m.parentRoomID(roomId)
	if parentRoomId == "" {
		return aiAgentStoredAvatarConfig{}
	}

	return storedAvatarConfig(m.loadRoomCustomAvatarConfigFor(ctx, parentRoomId))
}

// loadRoomCustomAvatarConfigFor reads the stored provider configuration of a
// single room, logging a lookup failure as a warning.
func (m *AIAgentModel) loadRoomCustomAvatarConfigFor(ctx context.Context, roomId string) *redisservice.AIAgentCustomProviderConfig {
	custom, err := m.redisService.LoadAIAgentCustomProvider(ctx, roomId)
	if err != nil {
		m.logger.WithError(err).WithField("room_id", roomId).Warn("could not read the custom avatar configuration")
		return nil
	}

	return custom
}

// resolveAIAgentAvatarConfig resolves the realtime avatar of a room from the
// request, the room's stored configuration and the server configuration.
// isCustom reports that the key belongs to the room instead of the server
// account.
func (m *AIAgentModel) resolveAIAgentAvatarConfig(ctx context.Context, roomId string, f *plugnmeet.InsightsAIAgentFeatures) (provider config.ProviderType, avatarID, apiKey, endpoint, authHeader string, isCustom bool, err error) {
	avatar := f.GetAvatar()
	if avatar == nil || !avatar.GetIsEnabled() {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	serviceCfg := m.aiAgentServiceConfig()
	// A room that already exists keeps its avatar provider and credentials in
	// Redis only. The lookup is lazy so a request that carries everything never
	// touches Redis.
	var stored aiAgentStoredAvatarConfig
	storedLoaded := false
	loadStored := func() aiAgentStoredAvatarConfig {
		if !storedLoaded {
			stored = m.loadRoomCustomAvatarConfig(ctx, roomId)
			storedLoaded = true
		}
		return stored
	}

	requestedProvider := strings.ToLower(strings.TrimSpace(avatar.GetProvider()))
	if requestedProvider == "" {
		requestedProvider = loadStored().Provider
	}
	provider = config.ProviderType(requestedProvider)
	if provider == "" && serviceCfg != nil {
		provider = config.ProviderType(strings.ToLower(strings.TrimSpace(serviceCfg.GetOptionsString("avatar_provider", ""))))
	}
	if !isAllowedAIAgentAvatarProvider(provider) {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	var account *config.ProviderAccount
	if m.appConfig != nil && m.appConfig.Insights != nil {
		accountID := ""
		if serviceCfg != nil {
			accountID = strings.TrimSpace(serviceCfg.GetOptionsString("avatar_account_id", ""))
		}
		// the room may run the avatar without any configured account
		account, _ = m.appConfig.Insights.GetProviderAccount(provider, accountID)
	}

	avatarID = strings.TrimSpace(avatar.GetAvatarId())
	if avatarID == "" && serviceCfg != nil {
		avatarID = strings.TrimSpace(serviceCfg.GetOptionsString("avatar_id", ""))
	}
	if avatarID == "" && account != nil {
		avatarID = strings.TrimSpace(account.GetOptionsString("avatar_id", ""))
	}
	if avatarID == "" {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	// At creation time the key is still part of the request; afterwards it lives
	// in Redis only, because the metadata is stripped before it is published.
	apiKey = strings.TrimSpace(avatar.GetApiKey())
	if apiKey == "" {
		apiKey = loadStored().APIKey
	}
	isCustom = apiKey != ""
	if apiKey == "" && account != nil {
		apiKey = strings.TrimSpace(account.Credentials.APIKey)
	}
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	// The gateway endpoint and the header that carries the key follow the same
	// fallback order as the rest: the request, then the room's stored
	// configuration, then the server account. Both are validated because a
	// stored value could predate a stricter rule.
	endpoint = strings.TrimSpace(avatar.GetEndpoint())
	if endpoint == "" {
		endpoint = loadStored().Endpoint
	}
	if endpoint == "" && account != nil {
		endpoint = strings.TrimSpace(account.GetOptionsString("endpoint", ""))
	}
	if endpoint != "" && !isAllowedAgentCustomProviderEndpoint(endpoint) {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	authHeader = strings.TrimSpace(avatar.GetAuthHeader())
	if authHeader == "" {
		authHeader = loadStored().AuthHeader
	}
	if authHeader == "" && account != nil {
		authHeader = strings.TrimSpace(account.GetOptionsString("auth_header", ""))
	}
	if authHeader != "" && !isAllowedAgentCustomProviderAuthHeader(authHeader) {
		return "", "", "", "", "", false, config.ErrAIAgentInvalidRequest
	}

	return provider, avatarID, apiKey, endpoint, authHeader, isCustom, nil
}

// aiAgentServiceConfig returns the server-configured ai_agent service, and nil
// when the server runs the AI agent with a room-supplied provider only.
func (m *AIAgentModel) aiAgentServiceConfig() *config.ServiceConfig {
	if m.appConfig == nil || m.appConfig.Insights == nil {
		return nil
	}
	_, serviceCfg, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent)
	if err != nil {
		return nil
	}

	return serviceCfg
}

// isAllowedAIAgentAvatarProvider reports whether the avatar provider is one the
// server can drive.
func isAllowedAIAgentAvatarProvider(provider config.ProviderType) bool {
	switch provider {
	case config.ProviderBeyondPresence, config.ProviderAnam, config.ProviderSimli:
		return true
	}

	return false
}

// parentRoomID returns the plain room id of a breakout room's parent, and "" for
// any other room.
func (m *AIAgentModel) parentRoomID(roomId string) string {
	if roomId == "" {
		return ""
	}
	meta, err := m.natsService.GetRoomMetadataStruct(roomId)
	if err != nil || !meta.GetIsBreakoutRoom() {
		return ""
	}
	parentRoomId, _, err := helpers.ParseBreakoutParentRoomRef(meta.GetParentRoomId())
	if err != nil || parentRoomId == roomId {
		return ""
	}

	return parentRoomId
}

// buildCustomProviderConfig turns the room's stored credentials into the account
// and service configuration the providers consume.
func (m *AIAgentModel) buildCustomProviderConfig(custom *redisservice.AIAgentCustomProviderConfig) (*config.ProviderAccount, *config.ServiceConfig) {
	provider := config.ProviderType(strings.ToLower(strings.TrimSpace(custom.Provider)))

	accountOptions := map[string]interface{}{}
	if endpoint := strings.TrimSpace(custom.Endpoint); endpoint != "" {
		accountOptions["endpoint"] = endpoint
	}
	if header := strings.TrimSpace(custom.AuthHeader); header != "" {
		accountOptions["auth_header"] = header
	}

	account := &config.ProviderAccount{
		ID:          m.customProviderAccountID(provider),
		Credentials: config.CredentialsConfig{APIKey: custom.APIKey},
		Options:     accountOptions,
	}
	serviceCfg := &config.ServiceConfig{
		Provider: provider,
		Options:  m.customProviderServiceOptions(provider, custom),
	}

	return account, serviceCfg
}

// customProviderAccountID labels the synthetic account so logs and metrics can
// tell a room-supplied provider from a configured one.
func (m *AIAgentModel) customProviderAccountID(provider config.ProviderType) string {
	return "room_custom_provider_" + string(provider)
}

// customProviderServiceOptions builds the model options of a custom provider: the
// host's overrides win over the server's own ai_agent options when it configures
// the same provider, and the provider defaults fill whatever is still missing.
func (m *AIAgentModel) customProviderServiceOptions(provider config.ProviderType, custom *redisservice.AIAgentCustomProviderConfig) map[string]interface{} {
	options := map[string]interface{}{}

	if m.appConfig.Insights != nil {
		if _, serviceCfg, err := m.appConfig.Insights.GetProviderAccountForService(insights.ServiceTypeAIAgent); err == nil &&
			serviceCfg != nil && serviceCfg.Provider == provider {
			for key, value := range serviceCfg.Options {
				options[key] = value
			}
		}
	}
	for key, value := range aiAgentCustomProviderModelDefaults(provider) {
		if _, ok := options[key]; !ok {
			options[key] = value
		}
	}

	if model := strings.TrimSpace(custom.RealtimeModel); model != "" {
		options["realtime_model"] = model
		options["model"] = model
		// a host override must also win over a server live model
		delete(options, "live_model")
	}
	if model := strings.TrimSpace(custom.DelegationModel); model != "" {
		options["delegation_model"] = model
	}
	if model := strings.TrimSpace(custom.SummarizeModel); model != "" {
		options["summarize_model"] = model
	}
	if header := strings.TrimSpace(custom.AuthHeader); header != "" {
		options["realtime_auth_header"] = header
	}

	return options
}

// aiAgentCustomProviderModelDefaults returns the models a provider serves when
// neither the server nor the host named one.
func aiAgentCustomProviderModelDefaults(provider config.ProviderType) map[string]string {
	switch provider {
	case config.ProviderGoogle:
		return map[string]string{
			"realtime_model":   aiAgentCustomGoogleLiveModel,
			"model":            aiAgentCustomGoogleLiveModel,
			"delegation_model": aiAgentCustomGoogleTextModel,
			"summarize_model":  aiAgentCustomGoogleTextModel,
		}
	case config.ProviderOpenAI:
		return map[string]string{
			"realtime_model":   aiAgentCustomOpenAILiveModel,
			"model":            aiAgentCustomOpenAILiveModel,
			"delegation_model": aiAgentCustomOpenAITextModel,
			"summarize_model":  aiAgentCustomOpenAITextModel,
		}
	}

	return nil
}
