package insightsservice

import (
	"context"
	"fmt"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/azure"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/google"
	"github.com/mynaparrot/plugnmeet-server/pkg/insights/providers/openai"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type ProviderArgs struct {
	Ctx             context.Context
	ProviderType    config.ProviderType
	ProviderAccount *config.ProviderAccount
	ServiceConfig   *config.ServiceConfig
	Logger          *logrus.Entry
	RDS             *redis.Client
}

// NewProvider is a factory function that creates and returns the configured AI provider.
func NewProvider(args *ProviderArgs) (insights.Provider, error) {
	log := args.Logger.WithFields(logrus.Fields{
		"provider": args.ProviderType,
	})
	switch args.ProviderType {
	case config.ProviderAzure:
		return azure.NewProvider(args.ProviderAccount, args.ServiceConfig, log)
	case config.ProviderGoogle:
		return google.NewProvider(args.Ctx, args.ProviderAccount, args.ServiceConfig, log)
	case config.ProviderOpenAI:
		return openai.NewProvider(args.Ctx, args.ProviderAccount, args.ServiceConfig, log, args.RDS)
	default:
		return nil, fmt.Errorf("unknown AI provider type: %s", args.ProviderType)
	}
}

type TaskArgs struct {
	Ctx             context.Context
	ServiceType     insights.ServiceType
	AppConf         *config.AppConfig
	NatsConn        *nats.Conn
	JS              jetstream.JetStream
	ServiceConfig   *config.ServiceConfig
	ProviderAccount *config.ProviderAccount
	NatsService     *natsservice.NatsService
	RedisService    *redisservice.RedisService
	Logger          *logrus.Entry
}

// NewTask is a factory that returns the correct Task implementation.
func NewTask(args *TaskArgs) (insights.Task, error) {
	switch args.ServiceType {
	case insights.ServiceTypeTranscription:
		return NewTranscriptionTask(args.AppConf, args.NatsConn, args.ServiceConfig, args.ProviderAccount, args.NatsService, args.RedisService, args.Logger)
	case insights.ServiceTypeTranslation:
		return NewTranslationTask(args.ServiceConfig, args.ProviderAccount, args.RedisService, args.Logger)
	case insights.ServiceTypeMeetingSummarizing:
		return NewMeetingSummarizingTask(args.Ctx, args.AppConf, args.JS, args.ServiceConfig, args.Logger)
	default:
		return nil, fmt.Errorf("unknown insights service task: %s", args.ServiceType)
	}
}
