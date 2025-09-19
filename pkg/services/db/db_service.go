package dbservice

import (
	"context"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type DatabaseService struct {
	db     *gorm.DB
	logger *logrus.Entry
}

func New(ctx context.Context, db *gorm.DB, logger *logrus.Logger) *DatabaseService {
	return &DatabaseService{
		db:     db.WithContext(ctx),
		logger: logger.WithField("service", "database"),
	}
}
