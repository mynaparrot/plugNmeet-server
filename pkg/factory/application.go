package factory

import (
	"context"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"sync"
)

// ApplicationServices holds all the shared services.
type ApplicationServices struct {
	RedisService    *redisservice.RedisService
	DatabaseService *dbservice.DatabaseService
	NatsService     *natsservice.NatsService
	LivekitService  *livekitservice.LivekitService
}

// ApplicationModels holds all the shared models.
type ApplicationModels struct {
	AnalyticsModel     *models.AnalyticsModel
	AuthModel          *models.AuthModel
	BBBApiWrapperModel *models.BBBApiWrapperModel
	BreakoutRoomModel  *models.BreakoutRoomModel
	RoomDurationModel  *models.RoomDurationModel
	EtherpadModel      *models.EtherpadModel
	ExDisplayModel     *models.ExDisplayModel
	ExMediaModel       *models.ExMediaModel
	FileModel          *models.FileModel
	IngressModel       *models.IngressModel
	LtiV1Model         *models.LtiV1Model
	NatsModel          *models.NatsModel
	PollModel          *models.PollModel
	RecorderModel      *models.RecorderModel
	RecordingModel     *models.RecordingModel
	RoomModel          *models.RoomModel
	SchedulerModel     *models.SchedulerModel
	SpeechToTextModel  *models.SpeechToTextModel
	UserModel          *models.UserModel
	WaitingRoomModel   *models.WaitingRoomModel
	WebhookModel       *models.WebhookModel
}

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
	SchedulerController    *controllers.SchedulerController
	SpeechToTextController *controllers.SpeechToTextController
	UserController         *controllers.UserController
	WaitingRoomController  *controllers.WaitingRoomController
	WebhookController      *controllers.WebhookController
	NatsController         *controllers.NatsController
}

// Application is the root struct holding all dependencies.
type Application struct {
	Services    *ApplicationServices
	Models      *ApplicationModels
	Controllers *ApplicationControllers
	AppConfig   *config.AppConfig
	Ctx         context.Context
}

func (a *Application) Boot() {
	var wg sync.WaitGroup
	// We need to wait for 1 critical setup task to complete.
	wg.Add(1)
	// Boot up the NATS controller in a goroutine.
	go a.Controllers.NatsController.BootUp(&wg)
	// Wait for NatsController.BootUp to finish its service registration.
	// This blocks until `wg.Done()` is called inside BootUp.
	wg.Wait()
	//a.Services.NatsService.
	// start scheduler
	go a.Controllers.SchedulerController.StartScheduler()
}
