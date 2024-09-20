package factory

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

func NewDatabaseConnection(appCnf *config.AppConfig) error {
	info := appCnf.DatabaseInfo
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=UTC", info.Username, info.Password, info.Host, info.Port, info.DBName)

	mysqlCnf := mysql.Config{
		DSN: dsn, // data source name
	}

	cnf := &gorm.Config{}

	if !appCnf.Client.Debug {
		newLogger := logger.New(
			config.GetLogger(),
			logger.Config{
				SlowThreshold:             time.Second, // Slow SQL threshold
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				ParameterizedQueries:      false,
				Colorful:                  false,
			},
		)
		cnf.Logger = newLogger
	} else {
		cnf.Logger = logger.Default.LogMode(logger.Info)
	}

	db, err := gorm.Open(mysql.New(mysqlCnf), cnf)
	if err != nil {
		return err
	}

	d, err := db.DB()
	if err != nil {
		return err
	}

	d.SetConnMaxLifetime(time.Minute * 4)
	d.SetMaxOpenConns(100)

	appCnf.DB = db
	return nil
}
