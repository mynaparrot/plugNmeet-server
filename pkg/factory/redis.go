package factory

import (
	"context"
	"crypto/tls"

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

	appCnf.RDS = rdb
	return nil
}
