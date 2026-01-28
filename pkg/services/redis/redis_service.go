package redisservice

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

const (
	Prefix          = "pnm:"
	DefaultTTL      = time.Hour * 24
	TotalUsageField = "total_usage"
)

type RedisService struct {
	ctx              context.Context
	rc               *redis.Client
	unlockScriptExec *redis.Script
	renewScriptExec  *redis.Script
	logger           *logrus.Entry
}

func New(ctx context.Context, rc *redis.Client, logger *logrus.Logger) *RedisService {
	return &RedisService{
		ctx:              ctx,
		rc:               rc,
		unlockScriptExec: redis.NewScript(unlockScript),
		renewScriptExec:  redis.NewScript(renewScript),
		logger:           logger.WithField("service", "redis"),
	}
}
