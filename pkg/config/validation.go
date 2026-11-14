package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
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
	return connections.ValidateDatabaseInfo(&appCnf.DatabaseInfo)
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
