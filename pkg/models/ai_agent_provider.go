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
	// Credentials never travel with the metadata, so they cannot leak through
	// NATS to the browsers; they are only accepted while creating the room.
	agentFeatures.CustomProvider = nil

	if custom == nil {
		if err := m.redisService.DeleteAIAgentCustomProvider(ctx, roomId); err != nil {
			m.logger.WithError(err).WithField("room_id", roomId).Debug("the stale custom ai provider configuration could not be removed")
		}
		return
	}

	err := m.redisService.SaveAIAgentCustomProvider(ctx, roomId, redisservice.AIAgentCustomProviderConfig{
		Provider:        custom.GetProvider(),
		APIKey:          custom.GetApiKey(),
		Endpoint:        custom.GetEndpoint(),
		AuthHeader:      custom.GetAuthHeader(),
		RealtimeModel:   custom.GetRealtimeModel(),
		DelegationModel: custom.GetDelegationModel(),
		SummarizeModel:  custom.GetSummarizeModel(),
	}, aiAgentCustomProviderTTL(agentFeatures.GetMaxSessionSeconds()))
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
	return m.validateAIAgentFeatures(f, e2ee, insightsAllowed, m.loadRoomCustomProvider(ctx, roomId) != nil)
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
