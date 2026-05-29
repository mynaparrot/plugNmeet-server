package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

func provideDBConnection(ctx context.Context, appCnf *config.AppConfig) (*gorm.DB, error) {
	log := appCnf.Logger.WithField("method", "provideDBConnection")
	info := appCnf.DatabaseInfo
	charset := "utf8mb4"
	loc := "UTC"
	connMaxLifetime := time.Minute * 4
	maxOpenConns := 10

	if info.Charset != nil && *info.Charset != "" {
		charset = *info.Charset
	}
	if info.Loc != nil && *info.Loc != "" {
		loc = strings.ReplaceAll(*info.Loc, "/", "%2F")
	}
	if info.ConnMaxLifetime != nil && *info.ConnMaxLifetime > 0 {
		connMaxLifetime = *info.ConnMaxLifetime
	}
	if info.MaxOpenConns != nil && *info.MaxOpenConns > 0 {
		maxOpenConns = *info.MaxOpenConns
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=%s", info.Username, info.Password, info.Host, info.Port, info.DBName, charset, loc)

	mysqlCnf := mysql.Config{
		DSN: dsn,
	}
	cnf := &gorm.Config{}

	loggerCnf := logger.Config{
		SlowThreshold:             time.Second,
		LogLevel:                  logger.Info,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      false,
		Colorful:                  true,
	}

	if !appCnf.Client.Debug {
		loggerCnf.LogLevel = logger.Warn
		cnf.Logger = logger.New(appCnf.Logger, loggerCnf)
	} else {
		cnf.Logger = logger.New(appCnf.Logger, loggerCnf)
	}

	db, err := gorm.Open(mysql.New(mysqlCnf), cnf)
	if err != nil {
		log.WithError(err).Error("failed to connect to database")
		return nil, err
	}

	if len(info.Replicas) > 0 {
		appCnf.Logger.Infof("Found %d read replicas, configuring dbresolver", len(info.Replicas))
		var replicaDialectors []gorm.Dialector

		for _, r := range info.Replicas {
			if r.Username == "" {
				r.Username = info.Username
			}
			if r.Password == "" {
				r.Password = info.Password
			}
			if r.Port == 0 {
				r.Port = info.Port
			}

			replicaDsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=True&loc=%s", r.Username, r.Password, r.Host, r.Port, info.DBName, charset, loc)
			replicaDialectors = append(replicaDialectors, mysql.Open(replicaDsn))
		}
		resolverCnf := dbresolver.Config{
			Replicas: replicaDialectors,
			Policy:   dbresolver.RandomPolicy{},
		}
		if appCnf.Client.Debug {
			resolverCnf.TraceResolverMode = true
		}

		err = db.Use(dbresolver.Register(resolverCnf).
			SetConnMaxLifetime(connMaxLifetime).
			SetMaxOpenConns(maxOpenConns).
			SetMaxIdleConns(maxOpenConns))
		if err != nil {
			log.WithError(err).Error("failed to configure dbresolver")
			return nil, err
		}
	}

	d, err := db.DB()
	if err != nil {
		log.WithError(err).Error("failed to get database instance")
		return nil, err
	}

	d.SetConnMaxLifetime(connMaxLifetime)
	d.SetMaxOpenConns(maxOpenConns)
	d.SetMaxIdleConns(maxOpenConns)

	err = d.PingContext(ctx)
	if err != nil {
		log.WithError(err).Error("failed to ping database")
		return nil, err
	}

	dbVersion := ""
	db.Raw("SELECT VERSION()").Scan(&dbVersion)
	log.WithField("version", dbVersion).Info("Successfully connected to database")

	return db, nil
}

func provideNATSConnection(appCnf *config.AppConfig) (*nats.Conn, error) {
	log := appCnf.Logger.WithField("method", "provideNATSConnection")
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

	return nc, nil
}

func provideJetStream(nc *nats.Conn) (jetstream.JetStream, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		// Assuming you want to log this, you'd need a logger.
		// For now, just returning the error.
		return nil, err
	}
	return js, nil
}

func provideRedisConnection(ctx context.Context, appCnf *config.AppConfig) (*redis.Client, error) {
	log := appCnf.Logger.WithField("method", "provideRedisConnection")
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
		log.WithError(err).Error("failed to connect to Redis")
		return nil, err
	}

	info, err := rdb.Info(ctx, "server").Result()
	if err == nil && info != "" {
		lines := strings.Split(info, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "redis_version:") {
				version := strings.TrimPrefix(line, "redis_version:")
				log.WithField("version", version).Info("Successfully connected to Redis")
				break
			}
		}
	}

	return rdb, nil
}

// populateAppCnfConnections ensures that any legacy code still accessing
// connections via the main config struct will not break.
func populateAppCnfConnections(appCnf *config.AppConfig, db *gorm.DB, rds *redis.Client, nc *nats.Conn, js jetstream.JetStream) {
	appCnf.DB = db
	appCnf.RDS = rds
	appCnf.NatsConn = nc
	appCnf.JetStream = js
}

var ConnectionModule = fx.Module("connections",
	// Providers for each connection type
	fx.Provide(
		provideDBConnection,
		provideRedisConnection,
		provideNATSConnection,
		provideJetStream,
	),
	// It runs after the connections are created and populates the appCnf struct.
	fx.Invoke(populateAppCnfConnections),
)
