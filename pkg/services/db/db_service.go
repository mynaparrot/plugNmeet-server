package dbservice

import (
	"context"

	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gorm.io/gorm"
)

type DatabaseService struct {
	db     *gorm.DB
	logger *logrus.Entry
}

type Args struct {
	fx.In
	Ctx    context.Context
	Db     *gorm.DB
	Logger *logrus.Logger
}

func New(args Args) *DatabaseService {
	s := &DatabaseService{
		db:     args.Db.WithContext(args.Ctx),
		logger: args.Logger.WithField("service", "database"),
	}

	return s
}

// AutoMigrate should be run after initializing of dbservice to ensure DB is ready before using it
// don't use OnStart hook for AutoMigrate
func (s *DatabaseService) AutoMigrate() error {
	log := s.logger.WithField("method", "AutoMigrate")
	err := s.db.AutoMigrate(&dbmodels.RoomInfo{}, &dbmodels.Recording{}, &dbmodels.RoomArtifact{}, &dbmodels.Analytics{})
	if err != nil {
		log.WithError(err).Error("Failed to migrate database")
		return err
	}
	return nil
}
