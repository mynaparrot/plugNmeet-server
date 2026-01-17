//go:build wireinject
// +build wireinject

package factory

import (
	"context"

	"github.com/google/wire"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

// build the dependency set for services
var serviceSet = wire.NewSet(
	dbservice.New,
	redisservice.New,
	natsservice.New,
	livekitservice.New,
)

// build the dependency set for helpers
var helperSet = wire.NewSet(
	helpers.GetWebhookNotifier,
)

func provideBreakoutRoomModel(rm *models.RoomModel, natsService *natsservice.NatsService) *models.BreakoutRoomModel {
	// we need to create BreakoutRoomModel first
	bm := models.NewBreakoutRoomModel(rm, natsService)
	// then set it to RoomModel to solve circular dependency
	rm.SetBreakoutRoomModel(bm)
	return bm
}

func provideArtifactModel(ctx context.Context, app *config.AppConfig, ds *dbservice.DatabaseService, redisService *redisservice.RedisService, natsService *natsservice.NatsService, webhookNotifier *helpers.WebhookNotifier, analyticsModel *models.AnalyticsModel) *models.ArtifactModel {
	// create the artifact model, which requires analyticsModel
	artifactModel := models.NewArtifactModel(ctx, app, ds, redisService, natsService, webhookNotifier, analyticsModel)
	// now complete the circular dependency by setting artifactModel on analyticsModel
	analyticsModel.SetArtifactModel(artifactModel)
	return artifactModel
}

// build the dependency set for models
var modelSet = wire.NewSet(
	models.NewAnalyticsModel,
	provideArtifactModel,
	models.NewAuthModel,
	models.NewInsightsModel,
	models.NewBBBApiWrapperModel,
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
	provideBreakoutRoomModel,
	models.NewJanitorModel,
	models.NewUserModel,
	models.NewWaitingRoomModel,
	models.NewWebhookModel,
)

// build the dependency set for controllers
var controllerSet = wire.NewSet(
	controllers.NewAnalyticsController,
	controllers.NewArtifactController,
	controllers.NewAuthController,
	controllers.NewBBBController,
	controllers.NewBreakoutRoomController,
	controllers.NewHealthCheckController,
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
	controllers.NewUserController,
	controllers.NewWaitingRoomController,
	controllers.NewWebhookController,
	controllers.NewNatsController,
	controllers.NewInsightsController,
)

// NewAppFactory is the injector function that wire will implement.
func NewAppFactory(ctx context.Context, appConfig *config.AppConfig) (*Application, error) {
	wire.Build(
		serviceSet,
		helperSet,
		modelSet,
		controllerSet,
		// Provide the whole AppConfig, and also specific fields needed by constructors.
		wire.FieldsOf(new(*config.AppConfig), "DB", "RDS", "Logger"),

		wire.Struct(new(ApplicationControllers), "*"),
		wire.Struct(new(Application), "*"),
	)
	return nil, nil // This return value is ignored.
}
