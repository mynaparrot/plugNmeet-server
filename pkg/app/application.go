package app

import (
	"context"
	"fmt"
	"time"

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
	fx.In
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
	ctx           context.Context
	log           *logrus.Entry
	shutDowner    fx.Shutdowner
	appConfig     *config.AppConfig
	router        *Router
	janitorModel  *models.JanitorModel
	artifactModel *models.ArtifactModel
	lkServices    *livekitservice.LivekitService
}

// NewApplication creates a new Application instance.
func NewApplication(
	ctx context.Context,
	shutDowner fx.Shutdowner,
	appConfig *config.AppConfig,
	janitorModel *models.JanitorModel,
	artifactModel *models.ArtifactModel,
	lkServices *livekitservice.LivekitService,
	router *Router,
	logger *logrus.Logger,
) *Application {
	return &Application{
		ctx:           ctx,
		shutDowner:    shutDowner,
		appConfig:     appConfig,
		janitorModel:  janitorModel,
		artifactModel: artifactModel,
		lkServices:    lkServices,
		router:        router,
		log:           logger.WithField("controller", "Application"),
	}
}

// RegisterHooks registers the application's lifecycle hooks with Fx.
func (a *Application) RegisterHooks(lifecycle fx.Lifecycle) {
	lifecycle.Append(fx.Hook{
		OnStart: a.Start,
		OnStop:  a.Stop,
	})
}

// Start is called when the application is starting. It must be non-blocking.
func (a *Application) Start(_ context.Context) error {
	log := a.log.WithFields(logrus.Fields{
		"method": "start",
	})

	// Perform synchronous, fallible startup steps first.
	if a.appConfig.LivekitSipInfo != nil && a.appConfig.LivekitSipInfo.Enabled {
		if err := a.lkServices.CreateSIPInboundTrunk(); err != nil {
			log.WithError(err).Error("Failed to create SIP inbound trunk")
			return err
		}
	}

	// Start the janitor in a separate goroutine.
	go a.janitorModel.StartJanitor()
	// TODO: will remove in future
	go a.artifactModel.MigrateAnalyticsToArtifacts()

	// Initialize NATS controller.
	if err := a.router.ctrl.NatsController.Initialize(); err != nil {
		log.WithError(err).Error("Failed to initialize NATS controller")
		return err
	}

	// Initialize Insights controller.
	if err := a.router.ctrl.InsightsController.Initialize(); err != nil {
		log.WithError(err).Error("Failed to initialize Insights controller")
		return err
	}

	// Start the HTTP server in a background goroutine.
	go func() {
		log.WithFields(logrus.Fields{
			"version": version.Version,
			"port":    a.appConfig.Client.Port,
		}).Info("Starting plugNmeet server")

		if err := a.router.fiberApp.Listen(fmt.Sprintf(":%d", a.appConfig.Client.Port)); err != nil {
			log.WithError(err).Error("HTTP server failed to start, initiating shutdown")
			// Use the Shutdowner to gracefully stop the entire Fx application.
			if shutdownErr := a.shutDowner.Shutdown(); shutdownErr != nil {
				log.WithError(shutdownErr).Error("Failed to gracefully shutdown")
			}
		}
	}()

	// OnStart must return nil to signal to Fx that the startup was successful.
	return nil
}

// Stop is called when the application is shutting down.
func (a *Application) Stop(_ context.Context) error {
	a.router.ctrl.NatsController.Stop()
	a.router.ctrl.InsightsController.Shutdown()
	a.router.ctrl.WebhookController.Shutdown()
	a.janitorModel.Shutdown()

	if err := a.router.fiberApp.ShutdownWithTimeout(15 * time.Second); err != nil {
		a.log.WithError(err).Warn("Graceful shutdown failed, forcing exit.")
	}

	return nil
}
