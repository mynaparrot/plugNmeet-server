package app

import (
	"context"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/connections"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

func provideDBConnection(lc fx.Lifecycle, ctx context.Context, appCnf *config.AppConfig, ll *logrus.Logger) (*gorm.DB, error) {
	log := ll.WithField("method", "provideDBConnection")

	db, err := connections.NewDatabaseConnection(ctx, &appCnf.DatabaseInfo, ll,
		connections.WithDebug(appCnf.Client.Debug))
	if err != nil {
		log.WithError(err).Error("failed to connect to database")
		return nil, err
	}

	lc.Append(fx.Hook{OnStop: func(_ context.Context) error {
		log.Info("Closing database connection")
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}})

	return db, nil
}

func provideNATSConnection(lc fx.Lifecycle, appCnf *config.AppConfig, ll *logrus.Logger) (*nats.Conn, error) {
	log := ll.WithField("method", "provideNATSConnection")
	info := appCnf.NatsInfo
	opts := []nats.Option{
		nats.Name("plugnmeet-server"),
	}

	if info.Nkey != nil {
		opt, err := utils.NkeyOptionFromSeedText(*info.Nkey)
		if err != nil {
			log.WithError(err).Error("failed to create nkey option")
			return nil, err
		}
		opts = append(opts, opt)
	} else {
		opt := nats.UserInfo(info.User, info.Password)
		opts = append(opts, opt)
	}

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), opts...)
	if err != nil {
		log.WithError(err).Error("failed to connect to NATS server")
		return nil, err
	}

	log.WithFields(logrus.Fields{
		"version": nc.ConnectedServerVersion(),
		"address": nc.ConnectedAddr(),
	}).Info("Successfully connected to NATS server")

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			log.Info("Draining NATS connection")
			if err := nc.Drain(); err != nil {
				return err
			}
			// Drain is asynchronous — wait for graceful close or context timeout.
			select {
			case <-nc.StatusChanged(nats.CLOSED):
				log.Info("NATS connection drained gracefully")
			case <-ctx.Done():
				log.Warn("NATS drain timed out, forcing close")
				nc.Close()
			}
			return nil
		},
	})

	return nc, nil
}

func provideJetStream(nc *nats.Conn, logger *logrus.Logger) (jetstream.JetStream, error) {
	log := logger.WithField("method", "provideJetStream")
	js, err := jetstream.New(nc)
	if err != nil {
		log.WithError(err).Error("failed to create jetstream context")
		return nil, err
	}
	return js, nil
}

func provideRedisConnection(lc fx.Lifecycle, ctx context.Context, appCnf *config.AppConfig, ll *logrus.Logger) (redis.UniversalClient, error) {
	log := ll.WithField("method", "provideRedisConnection")

	rdb, err := connections.NewRedisConnection(ctx, &appCnf.RedisInfo, ll)
	if err != nil {
		log.WithError(err).Error("failed to connect to Redis")
		return nil, err
	}

	lc.Append(fx.Hook{
		OnStop: func(_ context.Context) error {
			log.Info("Closing Redis connection")
			return rdb.Close()
		},
	})

	return rdb, nil
}

var ConnectionModule = fx.Module("connections",
	// Providers for each connection type
	fx.Provide(
		provideDBConnection,
		provideRedisConnection,
		provideNATSConnection,
		provideJetStream,
	),
)
