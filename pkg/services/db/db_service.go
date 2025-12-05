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
	s.autoMigrate()

	return s
}

func (s *DatabaseService) autoMigrate() {
	err := s.db.AutoMigrate(&dbmodels.RoomInfo{}, &dbmodels.Recording{}, &dbmodels.RoomArtifact{}, &dbmodels.Analytics{})
	if err != nil {
		s.logger.WithError(err).Error("failed to migrate database")
	}
}
