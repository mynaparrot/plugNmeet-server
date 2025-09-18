package factory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

func NewDatabaseConnection(ctx context.Context, appCnf *config.AppConfig) error {
	info := appCnf.DatabaseInfo
	charset := "utf8mb4"
	loc := "UTC"

	if info.Charset != nil && *info.Charset != "" {
		charset = *info.Charset
	}
	if info.Loc != nil && *info.Loc != "" {
		loc = strings.ReplaceAll(*info.Loc, "/", "%2F")
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

	if !appCnf.Client.Debug {
		loggerCnf.LogLevel = logger.Warn
		cnf.Logger = logger.New(appCnf.Logger, loggerCnf)
	} else {
		cnf.Logger = logger.New(appCnf.Logger, loggerCnf)
	}

	db, err := gorm.Open(mysql.New(mysqlCnf), cnf)
	if err != nil {
		return err
	}

	// If read replicas are configured, set up the dbresolver.
	if len(info.Replicas) > 0 {
		appCnf.Logger.Infof("found %d read replicas, configuring dbresolver", len(info.Replicas))
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
		if appCnf.Client.Debug {
			resolverCnf.TraceResolverMode = true
		}

		err = db.Use(dbresolver.Register(resolverCnf))
		if err != nil {
			return err
		}
	}

	d, err := db.DB()
	if err != nil {
		return err
	}
	err = d.PingContext(ctx)
	if err != nil {
		return err
	}

	connMaxLifetime := time.Minute * 4
	if info.ConnMaxLifetime != nil && *info.ConnMaxLifetime > 0 {
		connMaxLifetime = *info.ConnMaxLifetime
	}
	maxOpenConns := 10
	if info.MaxOpenConns != nil && *info.MaxOpenConns > 0 {
		maxOpenConns = *info.MaxOpenConns
	}

	// https://github.com/go-sql-driver/mysql?tab=readme-ov-file#important-settings
	d.SetConnMaxLifetime(connMaxLifetime)
	d.SetMaxOpenConns(maxOpenConns)
	d.SetMaxIdleConns(maxOpenConns)

	appCnf.DB = db
	return nil
}
