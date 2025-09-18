package dbservice

import (
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type DatabaseService struct {
	db     *gorm.DB
	logger *logrus.Entry
}

func New(db *gorm.DB, logger *logrus.Logger) *DatabaseService {
	return &DatabaseService{
		db:     db,
		logger: logger.WithField("service", "database"),
	}
}
