package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// ApplicationControllers holds all the controllers.
type ApplicationControllers struct {
	AnalyticsController    *controllers.AnalyticsController
	AuthController         *controllers.AuthController
	BBBController          *controllers.BBBController
	BreakoutRoomController *controllers.BreakoutRoomController
	EtherpadController     *controllers.EtherpadController
	FileController         *controllers.FileController
	LtiV1Controller        *controllers.LtiV1Controller
	PollsController        *controllers.PollsController
	RecordingController    *controllers.RecordingController
	RoomController         *controllers.RoomController
	UserController         *controllers.UserController
	WebhookController      *controllers.WebhookController
	NatsController         *controllers.NatsController
	HealthCheckController  *controllers.HealthCheckController
	InsightsController     *controllers.InsightsController
	ArtifactController     *controllers.ArtifactController
}

// Application is the root struct holding all dependencies for lifecycle management.
type Application struct {
	ctx                context.Context
	appConfig          *config.AppConfig
	httpServer         *fiber.App
	controllers        *ApplicationControllers
	natsController     *controllers.NatsController
	insightsController *controllers.InsightsController
	janitorModel       *models.JanitorModel
	artifactModel      *models.ArtifactModel
	lkServices         *livekitservice.LivekitService
}

// NewApplication creates a new Application instance.
func NewApplication(
	ctx context.Context,
	appConfig *config.AppConfig,
	controllers *ApplicationControllers,
	natsController *controllers.NatsController,
	insightsController *controllers.InsightsController,
	janitorModel *models.JanitorModel,
	artifactModel *models.ArtifactModel,
	lkServices *livekitservice.LivekitService,
) *Application {
	return &Application{
		ctx:                ctx,
		appConfig:          appConfig,
		httpServer:         newRouter(appConfig, controllers),
		controllers:        controllers,
		natsController:     natsController,
		insightsController: insightsController,
		janitorModel:       janitorModel,
		artifactModel:      artifactModel,
		lkServices:         lkServices,
	}
}

// RegisterHooks registers the application's lifecycle hooks with Fx.
func (a *Application) RegisterHooks(lifecycle fx.Lifecycle) {
	lifecycle.Append(fx.Hook{
		OnStart: a.Start,
		OnStop:  a.Stop,
	})
}

// Start is called when the application is starting.
func (a *Application) Start(ctx context.Context) error {
	if a.appConfig.LivekitSipInfo != nil && a.appConfig.LivekitSipInfo.Enabled {
		if err := a.lkServices.CreateSIPInboundTrunk(); err != nil {
			a.appConfig.Logger.WithError(err).Fatal("Failed to create SIP inbound trunk")
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go a.natsController.BootUp(a.ctx, &wg)
	wg.Wait()

	a.insightsController.StartSubscription()
	go a.janitorModel.StartJanitor()
	go a.artifactModel.MigrateAnalyticsToArtifacts()

	go func() {
		a.appConfig.Logger.WithFields(logrus.Fields{
			"version": version.Version,
			"port":    a.appConfig.Client.Port,
		}).Info("Starting plugNmeet server")

		if err := a.httpServer.Listen(fmt.Sprintf(":%d", a.appConfig.Client.Port)); err != nil {
			a.appConfig.Logger.WithError(err).Fatal("Failed to start server")
		}
	}()
	return nil
}

// Stop is called when the application is shutting down.
func (a *Application) Stop(ctx context.Context) error {
	a.insightsController.Shutdown()
	a.controllers.WebhookController.Shutdown()
	a.janitorModel.Shutdown()

	if err := a.httpServer.ShutdownWithTimeout(15 * time.Second); err != nil {
		a.appConfig.Logger.WithError(err).Warn("Graceful shutdown failed, forcing exit.")
	}

	if db, err := a.appConfig.DB.DB(); err == nil {
		_ = db.Close()
	}
	_ = a.appConfig.RDS.Close()
	_ = a.appConfig.NatsConn.Drain()
	a.appConfig.NatsConn.Close()

	logrus.Exit(0)
	return nil
}
