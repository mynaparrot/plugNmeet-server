//go:build wireinject
// +build wireinject

package factory

import (
	"context"
	"github.com/google/wire"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

func provideContext() context.Context {
	return context.Background()
}

// build the dependency set for services
var serviceSet = wire.NewSet(
	dbservice.New,
	redisservice.New,
	natsservice.New,
	livekitservice.New,
)

// build the dependency set for models
var modelSet = wire.NewSet(
	models.NewAnalyticsModel,
	models.NewAuthModel,
	models.NewBBBApiWrapperModel,
	models.NewBreakoutRoomModel,
	models.NewRoomDurationModel,
	models.NewEtherpadModel,
	models.NewExDisplayModel,
	models.NewExMediaModel,
	models.NewFileModel,
	models.NewIngressModel,
	models.NewLtiV1Model,
	models.NewNatsModel,
	models.NewPollModel,
	models.NewRecorderModel,
	models.NewRecordingModel,
	models.NewRoomModel,
	models.NewSchedulerModel,
	models.NewSpeechToTextModel,
	models.NewUserModel,
	models.NewWaitingRoomModel,
	models.NewWebhookModel,
)

// build the dependency set for controllers
var controllerSet = wire.NewSet(
	controllers.NewAnalyticsController,
	controllers.NewAuthController,
	controllers.NewBBBController,
	controllers.NewBreakoutRoomController,
	controllers.NewEtherpadController,
	controllers.NewExDisplayController,
	controllers.NewExMediaController,
	controllers.NewFileController,
	controllers.NewIngressController,
	controllers.NewLtiV1Controller,
	controllers.NewPollsController,
	controllers.NewRecorderController,
	controllers.NewRecordingController,
	controllers.NewRoomController,
	controllers.NewSchedulerController,
	controllers.NewSpeechToTextController,
	controllers.NewUserController,
	controllers.NewWaitingRoomController,
	controllers.NewWebhookController,
	controllers.NewNatsController,
)

// NewAppFactory is the injector function that wire will implement.
func NewAppFactory(appConfig *config.AppConfig) (*Application, error) {
	wire.Build(
		provideContext,
		serviceSet,
		modelSet,
		controllerSet,
		// Provide only the fields that are directly used by constructors.
		wire.FieldsOf(new(*config.AppConfig), "DB", "RDS"),

		wire.Struct(new(ApplicationServices), "*"),
		wire.Struct(new(ApplicationModels), "*"),
		wire.Struct(new(ApplicationControllers), "*"),
		wire.Struct(new(Application), "*"),
	)
	return nil, nil // This return value is ignored.
}
