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
		cnf.Logger = logger.New(config.GetLogger(), loggerCnf)
	} else {
		cnf.Logger = logger.New(config.GetLogger(), loggerCnf)
	}

	db, err := gorm.Open(mysql.New(mysqlCnf), cnf)
	if err != nil {
		return err
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
