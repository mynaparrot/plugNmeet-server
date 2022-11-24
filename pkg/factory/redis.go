package factory

import (
	"context"
	"crypto/tls"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
)

func NewRedisConnection() {
	var rdb *redis.Client
	var tlsConfig *tls.Config
	rf := config.AppCnf.RedisInfo

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

	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalln(err)
	}

	config.AppCnf.RDS = rdb
}
