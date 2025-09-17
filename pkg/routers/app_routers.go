package routers

import (
	"runtime"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	rr "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/version"
)

func New(appConfig *config.AppConfig, ctrl *factory.ApplicationControllers) *fiber.App {
	templateEngine := html.New(appConfig.Client.Path, ".html")

	if appConfig.Client.Debug {
		templateEngine.Reload(true)
		templateEngine.Debug(true)
	}

	cnf := fiber.Config{
		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,
		Views:       templateEngine,
		AppName:     "plugNmeet version: " + version.Version + " runtime: " + runtime.Version(),
	}

	if appConfig.Client.ProxyHeader != "" {
		cnf.ProxyHeader = appConfig.Client.ProxyHeader
	}

	app := fiber.New(cnf)

	if appConfig.Client.Debug {
		app.Use(logger.New())
	}
	if appConfig.Client.PrometheusConf.Enable {
		prometheus := fiberprometheus.New("plugNmeet")
		prometheus.RegisterAt(app, appConfig.Client.PrometheusConf.MetricsPath)
		app.Use(prometheus.Middleware)
	}
	app.Use(rr.New())
	app.Use(cors.New(cors.Config{
		AllowMethods: "POST,GET,OPTIONS",
	}))

	app.Static("/assets", config.GetConfig().Client.Path+"/assets")
	app.Static("/favicon.ico", config.GetConfig().Client.Path+"/assets/imgs/favicon.ico")

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", nil)
	})
	app.Get("/login*", func(c *fiber.Ctx) error {
		return c.Render("login", nil)
	})
	app.Post("/webhook", ctrl.WebhookController.HandleWebhook)
	app.Get("/download/uploadedFile/:sid/*", ctrl.FileController.HandleDownloadUploadedFile)
	app.Get("/download/recording/:token", ctrl.RecordingController.HandleDownloadRecording)
	app.Get("/download/analytics/:token", ctrl.AnalyticsController.HandleDownloadAnalytics)
	app.Get("/healthCheck", controllers.HandleHealthCheck)

	// lti group
	lti := app.Group("/lti")
	lti.Get("/v1", ctrl.LtiV1Controller.HandleLTIV1GETREQUEST)
	lti.Post("/v1", ctrl.LtiV1Controller.HandleLTIV1Landing)
	ltiV1API := lti.Group("/v1/api", ctrl.LtiV1Controller.HandleLTIV1VerifyHeaderToken)
	ltiV1API.Post("/room/join", ctrl.LtiV1Controller.HandleLTIV1JoinRoom)
	ltiV1API.Post("/room/isActive", ctrl.LtiV1Controller.HandleLTIV1IsRoomActive)
	ltiV1API.Post("/room/end", ctrl.LtiV1Controller.HandleLTIV1EndRoom)
	ltiV1API.Post("/recording/fetch", ctrl.LtiV1Controller.HandleLTIV1FetchRecordings)
	ltiV1API.Post("/recording/download", ctrl.LtiV1Controller.HandleLTIV1GetRecordingDownloadToken)
	ltiV1API.Post("/recording/delete", ctrl.LtiV1Controller.HandleLTIV1DeleteRecordings)

	auth := app.Group("/auth", ctrl.AuthController.HandleAuthHeaderCheck)
	auth.Post("/getClientFiles", ctrl.FileController.HandleGetClientFiles)

	// for room
	room := auth.Group("/room")
	room.Post("/create", ctrl.RoomController.HandleRoomCreate)
	room.Post("/getJoinToken", ctrl.UserController.HandleGenerateJoinToken)
	room.Post("/isRoomActive", ctrl.RoomController.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", ctrl.RoomController.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", ctrl.RoomController.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", ctrl.RoomController.HandleEndRoom)
	room.Post("/fetchPastRooms", ctrl.RoomController.HandleFetchPastRooms)

	// for recording
	recording := auth.Group("/recording")
	recording.Post("/fetch", ctrl.RecordingController.HandleFetchRecordings)
	recording.Post("/recordingInfo", ctrl.RecordingController.HandleRecordingInfo)
	recording.Post("/delete", ctrl.RecordingController.HandleDeleteRecording)
	recording.Post("/getDownloadToken", ctrl.RecordingController.HandleGetDownloadToken)

	// for analytics
	analytics := auth.Group("/analytics")
	analytics.Post("/fetch", ctrl.AnalyticsController.HandleFetchAnalytics)
	analytics.Post("/delete", ctrl.AnalyticsController.HandleDeleteAnalytics)
	analytics.Post("/getDownloadToken", ctrl.AnalyticsController.HandleGetAnalyticsDownloadToken)

	// to handle different events from recorder
	recorder := auth.Group("/recorder")
	recorder.Post("/notify", ctrl.RecorderController.HandleRecorderEvents)

	// for convert BBB request to PlugNmeet
	bbb := app.Group("/:apiKey/bigbluebutton/api", ctrl.BBBController.HandleVerifyApiRequest)
	bbb.All("/create", ctrl.BBBController.HandleBBBCreate)
	bbb.All("/join", ctrl.BBBController.HandleBBBJoin)
	bbb.All("/isMeetingRunning", ctrl.BBBController.HandleBBBIsMeetingRunning)
	bbb.All("/getMeetingInfo", ctrl.BBBController.HandleBBBGetMeetingInfo)
	bbb.All("/getMeetings", ctrl.BBBController.HandleBBBGetMeetings)
	bbb.All("/end", ctrl.BBBController.HandleBBBEndMeetings)
	bbb.All("/getRecordings", ctrl.BBBController.HandleBBBGetRecordings)
	bbb.All("/deleteRecordings", ctrl.BBBController.HandleBBBDeleteRecordings)
	// TO-DO: in the future
	bbb.All("/updateRecordings", ctrl.BBBController.HandleBBBUpdateRecordings)
	bbb.All("/publishRecordings", ctrl.BBBController.HandleBBBPublishRecordings)

	// api group will require sending token as Authorization header value
	api := app.Group("/api", ctrl.AuthController.HandleVerifyHeaderToken)
	api.Post("/verifyToken", ctrl.AuthController.HandleVerifyToken)

	api.Post("/recording", ctrl.RecorderController.HandleRecording)
	api.Post("/rtmp", ctrl.RecorderController.HandleRTMP)
	api.Post("/endRoom", ctrl.RoomController.HandleEndRoomForAPI)
	api.Post("/changeVisibility", ctrl.RoomController.HandleChangeVisibilityForAPI)
	api.Post("/convertWhiteboardFile", ctrl.FileController.HandleConvertWhiteboardFile)
	api.Post("/externalMediaPlayer", ctrl.ExMediaController.HandleExternalMediaPlayer)
	api.Post("/externalDisplayLink", ctrl.ExDisplayController.HandleExternalDisplayLink)

	api.Post("/updateLockSettings", ctrl.UserController.HandleUpdateUserLockSetting)
	api.Post("/muteUnmuteTrack", ctrl.UserController.HandleMuteUnMuteTrack)
	api.Post("/removeParticipant", ctrl.UserController.HandleRemoveParticipant)
	api.Post("/switchPresenter", ctrl.UserController.HandleSwitchPresenter)

	// etherpad group
	etherpad := api.Group("/etherpad")
	etherpad.Post("/create", ctrl.EtherpadController.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", ctrl.EtherpadController.HandleCleanPad)
	etherpad.Post("/changeStatus", ctrl.EtherpadController.HandleChangeEtherpadStatus)

	// waiting room group
	waitingRoom := api.Group("/waitingRoom")
	waitingRoom.Post("/approveUsers", ctrl.WaitingRoomController.HandleApproveUsers)
	waitingRoom.Post("/updateMsg", ctrl.WaitingRoomController.HandleUpdateWaitingRoomMessage)

	// polls group
	polls := api.Group("/polls")
	polls.Post("/activate", ctrl.PollsController.HandleActivatePolls)
	polls.Post("/create", ctrl.PollsController.HandleCreatePoll)
	polls.Get("/listPolls", ctrl.PollsController.HandleListPolls)
	polls.Get("/pollsStats", ctrl.PollsController.HandleGetPollsStats)
	polls.Get("/countTotalResponses/:pollId", ctrl.PollsController.HandleCountPollTotalResponses)
	polls.Get("/userSelectedOption/:pollId/:userId", ctrl.PollsController.HandleUserSelectedOption)
	polls.Get("/pollResponsesDetails/:pollId", ctrl.PollsController.HandleGetPollResponsesDetails)
	polls.Get("/pollResponsesResult/:pollId", ctrl.PollsController.HandleGetResponsesResult)
	polls.Post("/submitResponse", ctrl.PollsController.HandleUserSubmitResponse)
	polls.Post("/closePoll", ctrl.PollsController.HandleClosePoll)

	// breakout room group
	breakoutRoom := api.Group("/breakoutRoom")
	breakoutRoom.Post("/create", ctrl.BreakoutRoomController.HandleCreateBreakoutRooms)
	breakoutRoom.Post("/join", ctrl.BreakoutRoomController.HandleJoinBreakoutRoom)
	breakoutRoom.Get("/listRooms", ctrl.BreakoutRoomController.HandleGetBreakoutRooms)
	breakoutRoom.Get("/myRooms", ctrl.BreakoutRoomController.HandleGetMyBreakoutRooms)
	breakoutRoom.Post("/increaseDuration", ctrl.BreakoutRoomController.HandleIncreaseBreakoutRoomDuration)
	breakoutRoom.Post("/sendMsg", ctrl.BreakoutRoomController.HandleSendBreakoutRoomMsg)
	breakoutRoom.Post("/endRoom", ctrl.BreakoutRoomController.HandleEndBreakoutRoom)
	breakoutRoom.Post("/endAllRooms", ctrl.BreakoutRoomController.HandleEndBreakoutRooms)

	// Ingress
	ingress := api.Group("/ingress")
	ingress.Post("/create", ctrl.IngressController.HandleCreateIngress)

	// Speech services
	speech := api.Group("/speechServices")
	speech.Post("/serviceStatus", ctrl.SpeechToTextController.HandleSpeechToTextTranslationServiceStatus)
	speech.Post("/azureToken", ctrl.SpeechToTextController.HandleGenerateAzureToken)
	speech.Post("/userStatus", ctrl.SpeechToTextController.HandleSpeechServiceUserStatus)
	speech.Post("/renewToken", ctrl.SpeechToTextController.HandleRenewAzureToken)

	// for resumable.js need both GET and POST  methods.
	// https://github.com/23/resumable.js#how-do-i-set-it-up-with-my-server
	api.Get("/fileUpload", ctrl.FileController.HandleFileUpload)
	api.Post("/fileUpload", ctrl.FileController.HandleFileUpload)
	// as resumable.js will upload multiple parts of the file in different request
	// merging request should be sent from another request
	// otherwise hard to do it concurrently
	api.Post("/uploadedFileMerge", ctrl.FileController.HandleUploadedFileMerge)
	api.Post("/uploadBase64EncodedData", ctrl.FileController.HandleUploadBase64EncodedData)

	// last method
	app.Use(func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("not found")
	})

	return app
}
