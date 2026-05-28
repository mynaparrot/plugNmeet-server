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
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

type connection struct {
	appCnf *config.AppConfig
	ctx    context.Context
}

func InitConnections(ctx context.Context, appCnf *config.AppConfig) (*config.AppConfig, error) {
	c := &connection{
		appCnf: appCnf,
		ctx:    ctx,
	}

	if err := c.openDbConn(); err != nil {
		return nil, err
	}

	if err := c.openNatsConn(); err != nil {
		return nil, err
	}

	if err := c.openRedisConn(); err != nil {
		return nil, err
	}
	return c.appCnf, nil
}

func (c *connection) openDbConn() error {
	info := c.appCnf.DatabaseInfo
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
		DSN: dsn, // data source name
	}
	cnf := &gorm.Config{}

	loggerCnf := logger.Config{
		SlowThreshold:             time.Second, // Slow SQL threshold
		LogLevel:                  logger.Info,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      false,
		Colorful:                  true,
	}

	if !c.appCnf.Client.Debug {
		loggerCnf.LogLevel = logger.Warn
		cnf.Logger = logger.New(c.appCnf.Logger, loggerCnf)
	} else {
		cnf.Logger = logger.New(c.appCnf.Logger, loggerCnf)
	}

	db, err := gorm.Open(mysql.New(mysqlCnf), cnf)
	if err != nil {
		return err
	}

	// If read replicas are configured, set up the dbresolver.
	if len(info.Replicas) > 0 {
		c.appCnf.Logger.Infof("Found %d read replicas, configuring dbresolver", len(info.Replicas))
		var replicaDialectors []gorm.Dialector

		for _, r := range info.Replicas {
			// Use primary's settings as default for replicas if not specified.
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
			Policy:   dbresolver.RandomPolicy{}, // Use random policy to distribute read load.
		}
		if c.appCnf.Client.Debug {
			resolverCnf.TraceResolverMode = true
		}

		err = db.Use(dbresolver.Register(resolverCnf).
			SetConnMaxLifetime(connMaxLifetime).
			SetMaxOpenConns(maxOpenConns).
			SetMaxIdleConns(maxOpenConns))
		if err != nil {
			return err
		}
	}

	d, err := db.DB()
	if err != nil {
		return err
	}

	// https://github.com/go-sql-driver/mysql?tab=readme-ov-file#important-settings
	d.SetConnMaxLifetime(connMaxLifetime)
	d.SetMaxOpenConns(maxOpenConns)
	d.SetMaxIdleConns(maxOpenConns)

	err = d.PingContext(c.ctx)
	if err != nil {
		return err
	}

	dbVersion := ""
	db.Raw("SELECT VERSION()").Scan(&dbVersion)
	c.appCnf.Logger.WithField("version", dbVersion).Info("Successfully connected to database")

	c.appCnf.DB = db
	return nil
}

func (c *connection) openNatsConn() error {
	info := c.appCnf.NatsInfo
	var err error
	opts := []nats.Option{
		nats.Name("plugnmeet-server"),
	}

	if info.Nkey != nil {
		opt, err := utils.NkeyOptionFromSeedText(*info.Nkey)
		if err != nil {
			return err
		}
		opts = append(opts, opt)
	} else {
		opt := nats.UserInfo(info.User, info.Password)
		opts = append(opts, opt)
	}

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), opts...)
	if err != nil {
		return err
	}
	c.appCnf.NatsConn = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	c.appCnf.Logger.WithFields(logrus.Fields{
		"version": nc.ConnectedServerVersion(),
		"address": nc.ConnectedAddr(),
	}).Info("Successfully connected to NATS server")
	c.appCnf.JetStream = js

	return nil
}

func (c *connection) openRedisConn() error {
	rf := c.appCnf.RedisInfo
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

	_, err := rdb.Ping(c.ctx).Result()
	if err != nil {
		return err
	}

	info, err := rdb.Info(c.ctx, "server").Result()
	if err == nil && info != "" {
		lines := strings.Split(info, "\r\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "redis_version:") {
				version := strings.TrimPrefix(line, "redis_version:")
				c.appCnf.Logger.WithField("version", version).Info("Successfully connected to Redis")
				break
			}
		}
	}

	c.appCnf.RDS = rdb
	return nil
}
