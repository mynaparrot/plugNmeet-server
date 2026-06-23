package app

import (
	"context"
	"fmt"
	"os"

	lkLogger "github.com/livekit/protocol/logger"
	"github.com/mynaparrot/plugnmeet-protocol/logging"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	dbservice "github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	turnservice "github.com/mynaparrot/plugnmeet-server/pkg/services/turn"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

// provideAppConfig reads the config file and initializes the AppConfig.
func provideAppConfig(ctx context.Context, configFile string) (*config.AppConfig, error) {
	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
	}

	var appCnf config.AppConfig
	if err := yaml.Unmarshal(yamlFile, &appCnf); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config file %s: %w", configFile, err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}
	appCnf.RootWorkingDir = wd

	// Initialize the configuration, setting default values and creating necessary directories.
	initializedAppCnf, err := config.InitAppConfig(ctx, &appCnf)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize config: %w", err)
	}

	return initializedAppCnf, nil
}

// provideLogger initializes the application logger and livekit protocol logger.
func provideLogger(appCnf *config.AppConfig) (*logrus.Logger, error) {
	logger, err := logging.NewLogger(&appCnf.LogSettings)
	if err != nil {
		return nil, fmt.Errorf("failed to setup logger: %w", err)
	}

	// to avoid pion logs
	logConf := &lkLogger.Config{
		Level: "warn",
	}
	lkLogger.InitFromConfig(logConf, "pnm")

	return logger, nil
}

// ExecutePreStartTasks runs essential setup tasks that depend on core components
// like the application context, configuration, and logger.
// In the future, other pre-start logic can be added here.
func ExecutePreStartTasks(ctx context.Context, appCnf *config.AppConfig, logger *logrus.Logger) error {
	if appCnf.Hooks != nil {
		if err := appCnf.Hooks.InitializeHooks(ctx, appCnf.RootWorkingDir, logger); err != nil {
			logger.WithError(err).Error("failed to initialize hooks")
			return err
		}
	}
	return nil
}

var BootstrapModule = fx.Module("bootstrap",
	fx.Provide(provideAppConfig, provideLogger),
	fx.Invoke(ExecutePreStartTasks),
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
	),
)

var ApplicationModule = fx.Module("application",
	BootstrapModule,
	ConnectionModule,
	ServiceModule,
	HelperModule,
	ModelModule,
	ControllerModule,
	fx.Provide(NewRouter, NewApplication),
	fx.Invoke((*Application).RegisterHooks),
)
