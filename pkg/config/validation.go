package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
)

// validateAppConfig validates the external service configuration.
func validateAppConfig(appCnf *AppConfig) error {
	if err := validateLivekitInfo(&appCnf.LivekitInfo); err != nil {
		return err
	}
	if err := validateNatsInfo(&appCnf.NatsInfo); err != nil {
		return err
	}
	if err := connections.ValidateRedisInfo(&appCnf.RedisInfo); err != nil {
		return err
	}
	if err := connections.ValidateDatabaseInfo(&appCnf.DatabaseInfo); err != nil {
		return err
	}
	return validateInsightsConfig(appCnf.Insights)
}

// validateInsightsConfig rejects an ai_agent setup the provider cannot serve, so
// a misconfigured room fails at startup instead of on its first session.
func validateInsightsConfig(ic *InsightsConfig) error {
	if ic == nil || !ic.Enabled {
		return nil
	}

	aiAgent, ok := ic.Services[insights.ServiceTypeAIAgent]
	if !ok || aiAgent == nil {
		return nil
	}
	if _, _, err := ic.GetProviderAccountForService(insights.ServiceTypeAIAgent); err != nil {
		return err
	}
	if aiAgent.Provider != ProviderOpenAI {
		return nil
	}

	model := strings.TrimSpace(aiAgent.GetOptionsString("live_model", aiAgent.GetOptionsString("realtime_model", aiAgent.GetOptionsString("model", ""))))
	if !strings.HasPrefix(strings.ToLower(model), "gpt-live") && strings.TrimSpace(aiAgent.GetOptionsString("live_model", "")) == "" {
		return nil
	}
	if strings.TrimSpace(aiAgent.GetOptionsString("delegation_model", "")) == "" {
		return fmt.Errorf("insights.services.ai_agent: delegation_model is required when using OpenAI live model %q", model)
	}
	return nil
}

// validateLivekitInfo normalizes and validates the optional client-facing URL.
func validateLivekitInfo(lk *LivekitInfo) error {
	if lk.ClientHost == nil {
		return nil
	}

	v := strings.TrimSpace(*lk.ClientHost)
	if v == "" {
		lk.ClientHost = nil
		return nil
	}
	if err := validateURL("livekit_info.client_host", v, "http", "https", "ws", "wss"); err != nil {
		return err
	}

	lk.ClientHost = &v
	return nil
}

func validateNatsInfo(n *NatsInfo) error {
	if len(n.NatsUrls) == 0 {
		return fmt.Errorf("nats_info.nats_urls: at least one URL is required")
	}
	for i, raw := range n.NatsUrls {
		if err := validateURL(fmt.Sprintf("nats_info.nats_urls[%d]", i), raw, "nats", "tls", "ws", "wss"); err != nil {
			return err
		}
	}
	for i, raw := range n.NatsWSUrls {
		if err := validateURL(fmt.Sprintf("nats_info.nats_ws_urls[%d]", i), raw, "http", "https", "ws", "wss"); err != nil {
			return err
		}
	}
	if n.NumReplicas != 0 && !slices.Contains([]int{1, 3, 5}, n.NumReplicas) {
		return fmt.Errorf("nats_info.num_replicas %d: acceptable values are 1, 3 or 5", n.NumReplicas)
	}
	return nil
}

// validateURL ensures raw is an absolute URL with a host and an allowed scheme.
func validateURL(field, raw string, allowedSchemes ...string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s: %q must be a valid URL", field, raw)
	}
	if !slices.Contains(allowedSchemes, u.Scheme) {
		return fmt.Errorf("%s: %q scheme must be one of %s", field, raw, strings.Join(allowedSchemes, ", "))
	}
	return nil
}
