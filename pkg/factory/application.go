package factory

import (
	"context"
	"sync"

	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	livekitservice "github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
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
	lkServices    *livekitservice.LivekitService
}

func (a *Application) Boot() {
	if a.AppConfig.LivekitSipInfo != nil && a.AppConfig.LivekitSipInfo.Enabled {
		err := a.lkServices.CreateSIPInboundTrunk()
		if err != nil {
			a.AppConfig.Logger.WithError(err).Fatalln("Failed to create SIP inbound trunk")
		}
	}

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
