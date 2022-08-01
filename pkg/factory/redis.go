package factory

import (
	"context"
	"crypto/tls"
	"github.com/go-redis/redis/v8"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
)

var RDB *redis.Client

func NewRedisConnection() {
	var rdb *redis.Client
	var tlsConfig *tls.Config

	if config.AppCnf.RedisInfo.UseTLS {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}
	if config.AppCnf.RedisInfo.SentinelAddresses != nil {
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			SentinelAddrs:    config.AppCnf.RedisInfo.SentinelAddresses,
			SentinelUsername: config.AppCnf.RedisInfo.SentinelUsername,
			SentinelPassword: config.AppCnf.RedisInfo.SentinelPassword,
			MasterName:       config.AppCnf.RedisInfo.MasterName,
			Username:         config.AppCnf.RedisInfo.Username,
			Password:         config.AppCnf.RedisInfo.Password,
			DB:               config.AppCnf.RedisInfo.DBName,
			TLSConfig:        tlsConfig,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:      config.AppCnf.RedisInfo.Host,
			Username:  config.AppCnf.RedisInfo.Username,
			Password:  config.AppCnf.RedisInfo.Password,
			DB:        config.AppCnf.RedisInfo.DBName,
			TLSConfig: tlsConfig,
		})
	}

	_, err := rdb.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalln(err)
	}

	config.AppCnf.RDS = rdb
}

func SetRedisConnection(r *redis.Client) {
	_, err := r.Ping(context.Background()).Result()
	if err != nil {
		log.Fatalln(err)
	}
	RDB = r
}
