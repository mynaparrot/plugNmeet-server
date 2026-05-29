package app

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/turn"
	"go.uber.org/fx"
)

var ServiceModule = fx.Module("services",
	fx.Provide(
		dbservice.New,
		redisservice.New,
		natsservice.New,
		livekitservice.New,
		turnservice.New,
	),
	fx.Invoke((*dbservice.DatabaseService).AutoMigrate, (*natsservice.NatsService).Initialized),
)

var HelperModule = fx.Module("helpers",
	fx.Provide(
		helpers.NewWebhookNotifier,
	),
	fx.Invoke((*helpers.WebhookNotifier).SubscribeToCleanup),
)

// wireCircularModels is a dedicated Invoke function for wiring circular model dependencies.
func wireCircularModels(rm *models.RoomModel, bm *models.BreakoutRoomModel, analyticsModel *models.AnalyticsModel, artifactModel *models.ArtifactModel) {
	rm.SetBreakoutRoomModel(bm)
	analyticsModel.SetArtifactModel(artifactModel)
}

var ModelModule = fx.Module("models",
	fx.Provide(
		models.NewAnalyticsModel,
		models.NewArtifactModel,
		models.NewAuthModel,
		models.NewInsightsModel,
		models.NewBBBApiWrapperModel,
		models.NewEtherpadModel,
		models.NewFileModel,
		models.NewLtiV1Model,
		models.NewNatsModel,
		models.NewPollModel,
		models.NewRecordingModel,
		models.NewRoomModel,
		models.NewBreakoutRoomModel,
		models.NewJanitorModel,
		models.NewUserModel,
		models.NewWebhookModel,
	),
	fx.Invoke(wireCircularModels),
)

var ControllerModule = fx.Module("controllers",
	fx.Provide(
		controllers.NewAnalyticsController,
		controllers.NewArtifactController,
		controllers.NewAuthController,
		controllers.NewBBBController,
		controllers.NewBreakoutRoomController,
		controllers.NewHealthCheckController,
		controllers.NewEtherpadController,
		controllers.NewFileController,
		controllers.NewLtiV1Controller,
		controllers.NewPollsController,
		controllers.NewRecordingController,
		controllers.NewRoomController,
		controllers.NewUserController,
		controllers.NewWebhookController,
		controllers.NewNatsController,
		controllers.NewInsightsController,
		// Provide the struct that holds all controllers
		func(
			analyticsController *controllers.AnalyticsController,
			authController *controllers.AuthController,
			bbbController *controllers.BBBController,
			breakoutRoomController *controllers.BreakoutRoomController,
			etherpadController *controllers.EtherpadController,
			fileController *controllers.FileController,
			ltiV1Controller *controllers.LtiV1Controller,
			pollsController *controllers.PollsController,
			recordingController *controllers.RecordingController,
			roomController *controllers.RoomController,
			userController *controllers.UserController,
			webhookController *controllers.WebhookController,
			natsController *controllers.NatsController,
			healthCheckController *controllers.HealthCheckController,
			insightsController *controllers.InsightsController,
			artifactController *controllers.ArtifactController,
		) *ApplicationControllers {
			return &ApplicationControllers{
				AnalyticsController:    analyticsController,
				AuthController:         authController,
				BBBController:          bbbController,
				BreakoutRoomController: breakoutRoomController,
				EtherpadController:     etherpadController,
				FileController:         fileController,
				LtiV1Controller:        ltiV1Controller,
				PollsController:        pollsController,
				RecordingController:    recordingController,
				RoomController:         roomController,
				UserController:         userController,
				WebhookController:      webhookController,
				NatsController:         natsController,
				HealthCheckController:  healthCheckController,
				InsightsController:     insightsController,
				ArtifactController:     artifactController,
			}
		},
	),
)

var ApplicationModule = fx.Module("application",
	ConnectionModule,
	ServiceModule,
	HelperModule,
	ModelModule,
	ControllerModule,
	fx.Provide(NewApplication),
	fx.Invoke((*Application).RegisterHooks),
)
