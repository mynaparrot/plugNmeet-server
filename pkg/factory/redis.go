package factory

import (
	"context"
	"crypto/tls"
	"strings"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/redis/go-redis/v9"
)

func NewRedisConnection(ctx context.Context, appCnf *config.AppConfig) error {
	rf := appCnf.RedisInfo
	var rdb *redis.Client
	var tlsConfig *tls.Config

	if rf.UseTLS {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}
	if rf.SentinelAddresses != nil {
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			SentinelAddrs:    rf.SentinelAddresses,
			SentinelUsername: rf.SentinelUsername,
			SentinelPassword: rf.SentinelPassword,
			MasterName:       rf.MasterName,
			Username:         rf.Username,
			Password:         rf.Password,
			DB:               rf.DBName,
			TLSConfig:        tlsConfig,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:      rf.Host,
			Username:  rf.Username,
			Password:  rf.Password,
			DB:        rf.DBName,
			TLSConfig: tlsConfig,
		})
	}

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return err
	}

	info, err := rdb.Info(ctx, "server").Result()
	if err == nil && info != "" {
		lines := strings.Split(info, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "redis_version:") {
				version := strings.TrimPrefix(line, "redis_version:")
				appCnf.Logger.WithField("version", version).Info("successfully connected to Redis")
				break
			}
		}
	}

	appCnf.RDS = rdb
	return nil
}
