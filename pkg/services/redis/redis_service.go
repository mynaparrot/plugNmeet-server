package redisservice

import (
	"context"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	Prefix = "pnm:"
)

type RedisService struct {
	rc     *redis.Client
	ctx    context.Context
	logger *logrus.Entry
}

func New(rc *redis.Client, logger *logrus.Logger) *RedisService {
	return &RedisService{
		rc:     rc,
		ctx:    context.Background(),
		logger: logger.WithField("service", "redis"),
	}
}
