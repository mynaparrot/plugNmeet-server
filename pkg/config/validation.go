package config

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// validateAppConfig validates the external service configuration.
func validateAppConfig(appCnf *AppConfig) error {
	if err := validateLivekitInfo(&appCnf.LivekitInfo); err != nil {
		return err
	}
	if err := validateNatsInfo(&appCnf.NatsInfo); err != nil {
		return err
	}
	if err := validateRedisInfo(&appCnf.RedisInfo); err != nil {
		return err
	}
	return validateDatabaseInfo(&appCnf.DatabaseInfo)
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

func validateRedisInfo(r *RedisInfo) error {
	if len(r.SentinelAddresses) > 0 {
		if strings.TrimSpace(r.MasterName) == "" {
			return fmt.Errorf("redis_info.sentinel_master_name: required when sentinel_addresses is set")
		}
		for i, addr := range r.SentinelAddresses {
			if err := validateHostPort(fmt.Sprintf("redis_info.sentinel_addresses[%d]", i), addr); err != nil {
				return err
			}
		}
		return nil
	}
	return validateHostPort("redis_info.host", r.Host)
}

func validateDatabaseInfo(d *DatabaseInfo) error {
	if strings.TrimSpace(d.Host) == "" {
		return fmt.Errorf("database_info.host: required")
	}
	if err := validatePort("database_info.port", int(d.Port)); err != nil {
		return err
	}
	if strings.TrimSpace(d.DBName) == "" {
		return fmt.Errorf("database_info.db: required")
	}
	for i, r := range d.Replicas {
		if strings.TrimSpace(r.Host) == "" {
			return fmt.Errorf("database_info.replicas[%d].host: required", i)
		}
		if r.Port != 0 {
			if err := validatePort(fmt.Sprintf("database_info.replicas[%d].port", i), int(r.Port)); err != nil {
				return err
			}
		}
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

// validateHostPort ensures raw is in "host:port" format with a valid port.
func validateHostPort(field, raw string) error {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(raw))
	if err != nil || host == "" {
		return fmt.Errorf("%s: %q must be in host:port format", field, raw)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("%s: %q has an invalid port", field, raw)
	}
	return validatePort(field, port)
}

// validatePort ensures port is within the valid TCP range.
func validatePort(field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s: port %d must be between 1 and 65535", field, port)
	}
	return nil
}
