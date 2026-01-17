package factory

import (
	"context"
	"sync"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

// ApplicationControllers holds all the controllers.
type ApplicationControllers struct {
	AnalyticsController    *controllers.AnalyticsController
	AuthController         *controllers.AuthController
	BBBController          *controllers.BBBController
	BreakoutRoomController *controllers.BreakoutRoomController
	EtherpadController     *controllers.EtherpadController
	ExDisplayController    *controllers.ExDisplayController
	ExMediaController      *controllers.ExMediaController
	FileController         *controllers.FileController
	IngressController      *controllers.IngressController
	LtiV1Controller        *controllers.LtiV1Controller
	PollsController        *controllers.PollsController
	RecorderController     *controllers.RecorderController
	RecordingController    *controllers.RecordingController
	RoomController         *controllers.RoomController
	UserController         *controllers.UserController
	WaitingRoomController  *controllers.WaitingRoomController
	WebhookController      *controllers.WebhookController
	NatsController         *controllers.NatsController
	HealthCheckController  *controllers.HealthCheckController
	InsightsController     *controllers.InsightsController
	ArtifactController     *controllers.ArtifactController
}

// Application is the root struct holding all dependencies.
type Application struct {
	Controllers   *ApplicationControllers
	AppConfig     *config.AppConfig
	Ctx           context.Context
	janitorModel  *models.JanitorModel
	artifactModel *models.ArtifactModel
}

func (a *Application) Boot() {
	var wg sync.WaitGroup
	// We need to wait for authService setup task to complete.
	wg.Add(1)
	// Boot up the NATS controller in a goroutine.
	go a.Controllers.NatsController.BootUp(a.Ctx, &wg)
	// Wait for NatsController.BootUp to finish its service registration.
	// This blocks until `wg.Done()` is called inside BootUp.
	wg.Wait()

	a.Controllers.InsightsController.StartSubscription()
	go a.janitorModel.StartJanitor()

	// to migrate old analytics to new artifact
	// will be removed in the future
	go a.artifactModel.MigrateAnalyticsToArtifacts()
}

func (a *Application) Shutdown() {
	a.Controllers.InsightsController.Shutdown()
	a.janitorModel.Shutdown()
}
