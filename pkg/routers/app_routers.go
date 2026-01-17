package routers

import (
	"io"
	"runtime"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	rr "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/factory"
	"github.com/mynaparrot/plugnmeet-server/version"
)

// router is a struct to hold the dependencies for setting up routes,
// allowing us to break down the monolithic New() function into smaller,
// more manageable methods.
type router struct {
	app  *fiber.App
	ctrl *factory.ApplicationControllers
}

func New(appConfig *config.AppConfig, ctrl *factory.ApplicationControllers) *fiber.App {
	// --- Fiber App Configuration ---
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

	// --- App Initialization & Middleware ---
	app := fiber.New(cnf)

	app.Use(logger.New(logger.Config{
		Done: func(c *fiber.Ctx, logString []byte) {
			appConfig.Logger.Debugln(string(logString))
		},
		Format: "${status} | ${latency} | ${ip} | ${method} | ${path} | ${error}",
		Output: io.Discard,
	}))

	if appConfig.Client.PrometheusConf.Enable {
		prometheus := fiberprometheus.New("plugNmeet")
		prometheus.RegisterAt(app, appConfig.Client.PrometheusConf.MetricsPath)
		app.Use(prometheus.Middleware)
	}

	app.Use(rr.New())
	app.Use(cors.New(cors.Config{
		AllowMethods: "POST,GET,OPTIONS",
	}))
	app.Static("/assets", appConfig.Client.Path+"/assets")
	app.Static("/favicon.ico", appConfig.Client.Path+"/assets/imgs/favicon.ico")

	// --- Route Registration ---
	r := &router{
		app:  app,
		ctrl: ctrl,
	}

	r.registerBaseRoutes()
	r.registerLtiRoutes()
	r.registerAuthRoutes()
	r.registerBBBRoutes()
	r.registerAPIRoutes()

	// --- Final Catch-All 404 Handler ---
	// This MUST be the last middleware to be registered.
	app.Use(func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("not found")
	})

	return app
}

func (r *router) registerBaseRoutes() {
	r.app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", nil)
	})
	r.app.Get("/login*", func(c *fiber.Ctx) error {
		return c.Render("login", nil)
	})
	r.app.Post("/webhook", r.ctrl.WebhookController.HandleWebhook)
	r.app.Get("/download/uploadedFile/:sid/*", r.ctrl.FileController.HandleDownloadUploadedFile)
	r.app.Get("/download/recording/:token", r.ctrl.RecordingController.HandleDownloadRecording)
	r.app.Get("/download/analytics/:token", r.ctrl.AnalyticsController.HandleDownloadAnalytics)
	r.app.Get("/download/artifact/:token", r.ctrl.ArtifactController.HandleDownloadArtifact)
	r.app.Get("/healthCheck", r.ctrl.HealthCheckController.HandleHealthCheck)
}

func (r *router) registerLtiRoutes() {
	lti := r.app.Group("/lti")
	lti.Get("/v1", r.ctrl.LtiV1Controller.HandleLTIV1GETREQUEST)
	lti.Post("/v1", r.ctrl.LtiV1Controller.HandleLTIV1Landing)
	ltiV1API := lti.Group("/v1/api", r.ctrl.LtiV1Controller.HandleLTIV1VerifyHeaderToken)
	ltiV1API.Post("/room/join", r.ctrl.LtiV1Controller.HandleLTIV1JoinRoom)
	ltiV1API.Post("/room/isActive", r.ctrl.LtiV1Controller.HandleLTIV1IsRoomActive)
	ltiV1API.Post("/room/end", r.ctrl.LtiV1Controller.HandleLTIV1EndRoom)
	ltiV1API.Post("/recording/fetch", r.ctrl.LtiV1Controller.HandleLTIV1FetchRecordings)
	ltiV1API.Post("/recording/download", r.ctrl.LtiV1Controller.HandleLTIV1GetRecordingDownloadToken)
	ltiV1API.Post("/recording/delete", r.ctrl.LtiV1Controller.HandleLTIV1DeleteRecordings)
}

func (r *router) registerAuthRoutes() {
	auth := r.app.Group("/auth", r.ctrl.AuthController.HandleAuthHeaderCheck)
	auth.Post("/getClientFiles", r.ctrl.FileController.HandleGetClientFiles)

	room := auth.Group("/room")
	room.Post("/create", r.ctrl.RoomController.HandleRoomCreate)
	room.Post("/getJoinToken", r.ctrl.UserController.HandleGenerateJoinToken)
	room.Post("/isRoomActive", r.ctrl.RoomController.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", r.ctrl.RoomController.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", r.ctrl.RoomController.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", r.ctrl.RoomController.HandleEndRoom)
	room.Post("/fetchPastRooms", r.ctrl.RoomController.HandleFetchPastRooms)

	recording := auth.Group("/recording")
	recording.Post("/fetch", r.ctrl.RecordingController.HandleFetchRecordings)
	recording.Post("/info", r.ctrl.RecordingController.HandleRecordingInfo)
	recording.Post("/updateMetadata", r.ctrl.RecordingController.HandleUpdateRecordingMetadata)
	recording.Post("/delete", r.ctrl.RecordingController.HandleDeleteRecording)
	recording.Post("/getDownloadToken", r.ctrl.RecordingController.HandleGetDownloadToken)
	// deprecated: use /info
	recording.Post("/recordingInfo", r.ctrl.RecordingController.HandleRecordingInfo)

	analytics := auth.Group("/analytics")
	analytics.Post("/fetch", r.ctrl.AnalyticsController.HandleFetchAnalytics)
	analytics.Post("/delete", r.ctrl.AnalyticsController.HandleDeleteAnalytics)
	analytics.Post("/getDownloadToken", r.ctrl.AnalyticsController.HandleGetAnalyticsDownloadToken)

	artifact := auth.Group("/artifact")
	artifact.Post("/fetch", r.ctrl.ArtifactController.HandleFetchArtifacts)
	artifact.Post("/info", r.ctrl.ArtifactController.HandleGetArtifactInfo)
	artifact.Post("/delete", r.ctrl.ArtifactController.HandleDeleteArtifact)
	artifact.Post("/getDownloadToken", r.ctrl.ArtifactController.HandleGetArtifactDownloadToken)

	recorder := auth.Group("/recorder")
	recorder.Post("/notify", r.ctrl.RecorderController.HandleRecorderEvents)
}

func (r *router) registerBBBRoutes() {
	bbb := r.app.Group("/:apiKey/bigbluebutton/api", r.ctrl.BBBController.HandleVerifyApiRequest)
	bbb.All("/create", r.ctrl.BBBController.HandleBBBCreate)
	bbb.All("/join", r.ctrl.BBBController.HandleBBBJoin)
	bbb.All("/isMeetingRunning", r.ctrl.BBBController.HandleBBBIsMeetingRunning)
	bbb.All("/getMeetingInfo", r.ctrl.BBBController.HandleBBBGetMeetingInfo)
	bbb.All("/getMeetings", r.ctrl.BBBController.HandleBBBGetMeetings)
	bbb.All("/end", r.ctrl.BBBController.HandleBBBEndMeetings)
	bbb.All("/getRecordings", r.ctrl.BBBController.HandleBBBGetRecordings)
	bbb.All("/deleteRecordings", r.ctrl.BBBController.HandleBBBDeleteRecordings)
	bbb.All("/updateRecordings", r.ctrl.BBBController.HandleBBBUpdateRecordings)
	bbb.All("/publishRecordings", r.ctrl.BBBController.HandleBBBPublishRecordings)
}

func (r *router) registerInsightsRegisterAPIRoutes(api fiber.Router) {
	insights := api.Group("/insights")
	insights.Post("/supportedLangs", r.ctrl.InsightsController.HandleGetSupportedLangs)

	transcription := insights.Group("/transcription")
	transcription.Post("/configure", r.ctrl.InsightsController.HandleTranscriptionConfigure)
	transcription.Post("/end", r.ctrl.InsightsController.HandleEndTranscription)
	transcription.Post("/userSession", r.ctrl.InsightsController.HandleTranscriptionUserSession)
	transcription.Post("/userStatus", r.ctrl.InsightsController.HandleGetTranscriptionUserTaskStatus)

	translation := insights.Group("/translation")
	chatTranslation := translation.Group("/chat")
	chatTranslation.Post("/configure", r.ctrl.InsightsController.HandleChatTranslationConfigure)
	chatTranslation.Post("/end", r.ctrl.InsightsController.HandleEndChatTranslation)
	chatTranslation.Post("/execute", r.ctrl.InsightsController.HandleExecuteChatTranslation)

	ai := insights.Group("/ai")

	aiTextChat := ai.Group("/textChat")
	aiTextChat.Post("/configure", r.ctrl.InsightsController.HandleAITextChatConfigure)
	aiTextChat.Post("/execute", r.ctrl.InsightsController.HandleExecuteAITextChat)
	aiTextChat.Post("/end", r.ctrl.InsightsController.HandleEndAITextChat)

	aiMeetingSummarization := ai.Group("/meetingSummarization")
	aiMeetingSummarization.Post("/configure", r.ctrl.InsightsController.HandleAIMeetingSummarizationConfig)
	aiMeetingSummarization.Post("/end", r.ctrl.InsightsController.HandleEndAIMeetingSummarization)
}

func (r *router) registerAPIRoutes() {
	api := r.app.Group("/api", r.ctrl.AuthController.HandleVerifyHeaderToken)
	api.Post("/verifyToken", r.ctrl.AuthController.HandleVerifyToken)

	api.Post("/recording", r.ctrl.RecorderController.HandleRecording)
	api.Post("/rtmp", r.ctrl.RecorderController.HandleRTMP)
	api.Post("/endRoom", r.ctrl.RoomController.HandleEndRoomForAPI)
	api.Post("/changeVisibility", r.ctrl.RoomController.HandleChangeVisibilityForAPI)
	api.Post("/convertWhiteboardFile", r.ctrl.FileController.HandleConvertWhiteboardFile)
	api.Post("/externalMediaPlayer", r.ctrl.ExMediaController.HandleExternalMediaPlayer)
	api.Post("/externalDisplayLink", r.ctrl.ExDisplayController.HandleExternalDisplayLink)
	api.Post("/updateLockSettings", r.ctrl.UserController.HandleUpdateUserLockSetting)
	api.Post("/muteUnmuteTrack", r.ctrl.UserController.HandleMuteUnMuteTrack)
	api.Post("/removeParticipant", r.ctrl.UserController.HandleRemoveParticipant)
	api.Post("/switchPresenter", r.ctrl.UserController.HandleSwitchPresenter)

	etherpad := api.Group("/etherpad")
	etherpad.Post("/create", r.ctrl.EtherpadController.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", r.ctrl.EtherpadController.HandleCleanPad)
	etherpad.Post("/changeStatus", r.ctrl.EtherpadController.HandleChangeEtherpadStatus)

	waitingRoom := api.Group("/waitingRoom")
	waitingRoom.Post("/approveUsers", r.ctrl.WaitingRoomController.HandleApproveUsers)
	waitingRoom.Post("/updateMsg", r.ctrl.WaitingRoomController.HandleUpdateWaitingRoomMessage)

	polls := api.Group("/polls")
	polls.Post("/activate", r.ctrl.PollsController.HandleActivatePolls)
	polls.Post("/create", r.ctrl.PollsController.HandleCreatePoll)
	polls.Get("/listPolls", r.ctrl.PollsController.HandleListPolls)
	polls.Get("/pollsStats", r.ctrl.PollsController.HandleGetPollsStats)
	polls.Get("/countTotalResponses/:pollId", r.ctrl.PollsController.HandleCountPollTotalResponses)
	polls.Get("/userSelectedOption/:pollId/:userId", r.ctrl.PollsController.HandleUserSelectedOption)
	polls.Get("/pollResponsesDetails/:pollId", r.ctrl.PollsController.HandleGetPollResponsesDetails)
	polls.Get("/pollResponsesResult/:pollId", r.ctrl.PollsController.HandleGetResponsesResult)
	polls.Post("/submitResponse", r.ctrl.PollsController.HandleUserSubmitResponse)
	polls.Post("/closePoll", r.ctrl.PollsController.HandleClosePoll)

	breakoutRoom := api.Group("/breakoutRoom")
	breakoutRoom.Post("/create", r.ctrl.BreakoutRoomController.HandleCreateBreakoutRooms)
	breakoutRoom.Post("/join", r.ctrl.BreakoutRoomController.HandleJoinBreakoutRoom)
	breakoutRoom.Get("/listRooms", r.ctrl.BreakoutRoomController.HandleGetBreakoutRooms)
	breakoutRoom.Get("/myRooms", r.ctrl.BreakoutRoomController.HandleGetMyBreakoutRooms)
	breakoutRoom.Post("/increaseDuration", r.ctrl.BreakoutRoomController.HandleIncreaseBreakoutRoomDuration)
	breakoutRoom.Post("/sendMsg", r.ctrl.BreakoutRoomController.HandleSendBreakoutRoomMsg)
	breakoutRoom.Post("/endRoom", r.ctrl.BreakoutRoomController.HandleEndBreakoutRoom)
	breakoutRoom.Post("/endAllRooms", r.ctrl.BreakoutRoomController.HandleEndBreakoutRooms)

	ingress := api.Group("/ingress")
	ingress.Post("/create", r.ctrl.IngressController.HandleCreateIngress)

	// insights AI routers
	r.registerInsightsRegisterAPIRoutes(api)

	// for resumable.js need both GET and POST  methods.
	// https://github.com/23/resumable.js#how-do-i-set-it-up-with-my-server
	api.Get("/fileUpload", r.ctrl.FileController.HandleFileUpload)
	api.Post("/fileUpload", r.ctrl.FileController.HandleFileUpload)
	// as resumable.js will upload multiple parts of the file in different request
	// merging request should be sent from another request
	// otherwise hard to do it concurrently
	api.Post("/uploadedFileMerge", r.ctrl.FileController.HandleUploadedFileMerge)
	// uploadBase64EncodedData will accept raw base64 data of files
	// mostly for whiteboard images
	api.Post("/uploadBase64EncodedData", r.ctrl.FileController.HandleUploadBase64EncodedData)
	api.All("/getRoomFilesByType", r.ctrl.FileController.HandleGetRoomFilesByType)
}
