package dbservice

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type DatabaseService struct {
	db     *gorm.DB
	logger *logrus.Entry
}

func New(ctx context.Context, db *gorm.DB, logger *logrus.Logger) *DatabaseService {
	s := &DatabaseService{
		db:     db.WithContext(ctx),
		logger: logger.WithField("service", "database"),
	}

	return s
}

func (s *DatabaseService) AutoMigrate() error {
	log := s.logger.WithField("method", "AutoMigrate")
	err := s.db.AutoMigrate(&dbmodels.RoomInfo{}, &dbmodels.Recording{}, &dbmodels.RoomArtifact{}, &dbmodels.Analytics{})
	if err != nil {
		log.WithError(err).Error("Failed to migrate database")
		return err
	}
	return nil
}
