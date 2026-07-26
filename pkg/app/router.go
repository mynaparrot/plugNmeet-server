package app

import (
	"io"
	"path"
	"runtime"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/gofiber/fiber/v3/middleware/basicauth"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/favicon"
	"github.com/gofiber/fiber/v3/middleware/logger"
	rr "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/gofiber/template/html/v3"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

// Router is a struct to hold the dependencies for setting up routes
type Router struct {
	fiberApp *fiber.App
	ctrl     ApplicationControllers
}

func NewRouter(appConfig *config.AppConfig, ctrl ApplicationControllers, ll *logrus.Logger) *Router {
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
		BodyLimit:   int(appConfig.UploadFileSettings.MaxSize * 1024 * 1024),
	}

	if appConfig.Client.ProxyConf != nil && appConfig.Client.ProxyConf.Enabled {
		cnf.ProxyHeader = appConfig.Client.ProxyConf.ProxyHeader
		cnf.TrustProxy = true
		cnf.TrustProxyConfig.Proxies = appConfig.Client.ProxyConf.TrustedProxyIps
	}

	// --- App Initialization & Middleware ---
	app := fiber.New(cnf)
	app.Use(rr.New())

	// serving static files from assets dir
	assets := path.Join(appConfig.Client.Path, "assets")
	app.Use("/assets", static.New(assets))
	app.Use(favicon.New(favicon.Config{
		File: path.Join(assets, "imgs", "favicon.ico"),
	}))

	app.Use(logger.New(logger.Config{
		Done: func(c fiber.Ctx, logString []byte) {
			ll.Debugln(string(logString))
		},
		Format:      "| ${status} | ${latency} | ${ip} | ${method} | ${path} | ${error}",
		ForceColors: true,
		Stream:      io.Discard,
	}))

	prometheusConf := appConfig.Client.PrometheusConf
	if prometheusConf.Enable {
		p := prometheusConf.MetricsPath
		if p == "" {
			p = "/metrics"
		}
		if prometheusConf.Username != "" && prometheusConf.Password != "" {
			app.Use(p, basicauth.New(basicauth.Config{
				Authorizer: func(user, pass string, c fiber.Ctx) bool {
					return user == prometheusConf.Username && pass == prometheusConf.Password
				},
			}))
		}
		app.Use(p, adaptor.HTTPHandler(promhttp.Handler()))
	}

	app.Use(cors.New(cors.Config{
		AllowMethods: []string{"POST", "GET", "OPTIONS", "HEAD"},
	}))

	// --- Route Registration ---
	r := &Router{
		fiberApp: app,
		ctrl:     ctrl,
	}

	r.registerBaseRoutes()
	r.registerLtiRoutes()
	r.registerAuthRoutes()
	r.registerBBBRoutes()
	r.registerAPIRoutes()

	// --- Final Catch-All 404 Handler ---
	// This MUST be the last middleware to be registered.
	r.fiberApp.Use(func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("not found")
	})

	return r
}

func (r *Router) registerBaseRoutes() {
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/", func(c fiber.Ctx) error {
		return c.Render("index", nil)
	})
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/login*", func(c fiber.Ctx) error {
		return c.Render("login", nil)
	})
	r.fiberApp.Post("/webhook", r.ctrl.WebhookController.HandleWebhook)
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/download/uploadedFile/*", r.ctrl.FileController.HandleDownloadUploadedFile)
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/download/recording/:token", r.ctrl.RecordingController.HandleDownloadRecording)
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/download/analytics/:token", r.ctrl.AnalyticsController.HandleDownloadAnalytics)
	r.fiberApp.Add([]string{"GET", "HEAD"}, "/download/artifact/:token", r.ctrl.ArtifactController.HandleDownloadArtifact)
	r.fiberApp.Use("/healthCheck", r.ctrl.HealthCheckController.HandleHealthCheck)
}

func (r *Router) registerLtiRoutes() {
	lti := r.fiberApp.Group("/lti")
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

func (r *Router) registerAuthRoutes() {
	auth := r.fiberApp.Group("/auth", r.ctrl.AuthController.HandleAuthHeaderCheck)
	auth.Post("/getClientFiles", r.ctrl.FileController.HandleGetClientFiles)

	room := auth.Group("/room")
	room.Post("/create", r.ctrl.RoomController.HandleRoomCreate)
	room.Post("/getJoinToken", r.ctrl.UserController.HandleGenerateJoinToken)
	room.Post("/isRoomActive", r.ctrl.RoomController.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", r.ctrl.RoomController.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", r.ctrl.RoomController.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", r.ctrl.RoomController.HandleEndRoom)
	room.Post("/fetchPastRooms", r.ctrl.RoomController.HandleFetchPastRooms)
	room.Post("/broadcastToRoom", r.ctrl.RoomController.HandleBroadcastToRoom)
	room.Post("/uploadWhiteboardFile", r.ctrl.FileController.HandleUploadWhiteboardFile)

	recording := auth.Group("/recording")
	recording.Post("/fetch", r.ctrl.RecordingController.HandleFetchRecordings)
	recording.Post("/info", r.ctrl.RecordingController.HandleRecordingInfo)
	recording.Post("/updateMetadata", r.ctrl.RecordingController.HandleUpdateRecordingMetadata)
	recording.Post("/mergeRecordings", r.ctrl.RecordingController.HandleMergeRecordings)
	recording.Post("/delete", r.ctrl.RecordingController.HandleDeleteRecording)
	recording.Post("/getDownloadToken", r.ctrl.RecordingController.HandleGetDownloadToken)
	// TODO: remove deprecated: use /info
	recording.Post("/recordingInfo", r.ctrl.RecordingController.HandleRecordingInfo)

	// TODO: remove deprecated handler
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
	recorder.Post("/notify", r.ctrl.RecordingController.HandleRecorderEvents)
}

func (r *Router) registerBBBRoutes() {
	bbb := r.fiberApp.Group("/:apiKey/bigbluebutton/api", r.ctrl.BBBController.HandleVerifyApiRequest)
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

func (r *Router) registerAPIRoutes() {
	api := r.fiberApp.Group("/api", r.ctrl.AuthController.HandleVerifyHeaderToken)
	api.Post("/verifyToken", r.ctrl.AuthController.HandleVerifyToken)
	api.Post("/getClientFiles", r.ctrl.FileController.HandleGetClientFiles)

	api.Post("/recording", r.ctrl.RecordingController.HandleRecorderTasks)
	api.Post("/rtmp", r.ctrl.RecordingController.HandleRecorderTasks)

	api.Post("/endRoom", r.ctrl.RoomController.HandleEndRoomForAPI)
	api.Post("/changeVisibility", r.ctrl.RoomController.HandleChangeVisibilityForAPI)
	api.Post("/enableSipDialIn", r.ctrl.RoomController.HandleEnableRoomSipDialIn)
	api.Post("/externalDisplayLink", r.ctrl.RoomController.HandleExternalDisplayLink)
	api.Post("/externalMediaPlayer", r.ctrl.RoomController.HandleExternalMediaPlayer)

	ingress := api.Group("/ingress")
	ingress.Post("/create", r.ctrl.RoomController.HandleCreateIngress)

	waitingRoom := api.Group("/waitingRoom")
	waitingRoom.Post("/approveUsers", r.ctrl.RoomController.HandleApproveUsers)
	waitingRoom.Post("/updateMsg", r.ctrl.RoomController.HandleUpdateWaitingRoomMessage)

	api.Post("/updateLockSettings", r.ctrl.UserController.HandleUpdateUserLockSetting)
	api.Post("/muteUnmuteTrack", r.ctrl.UserController.HandleMuteUnMuteTrack)
	api.Post("/removeParticipant", r.ctrl.UserController.HandleRemoveParticipant)
	api.Post("/switchPresenter", r.ctrl.UserController.HandleSwitchPresenter)

	etherpad := api.Group("/etherpad")
	etherpad.Post("/create", r.ctrl.EtherpadController.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", r.ctrl.EtherpadController.HandleCleanPad)
	etherpad.Post("/changeStatus", r.ctrl.EtherpadController.HandleChangeEtherpadStatus)

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

	// TODO: remove in next release
	// deprecated
	api.Post("/convertWhiteboardFile", r.ctrl.FileController.HandleConvertWhiteboardFile)

	whiteboard := api.Group("/whiteboard")
	whiteboard.Post("/convert", r.ctrl.FileController.HandleConvertWhiteboardFile)
	whiteboard.Post("/pdf-export/upload", r.ctrl.FileController.HandleWhiteboardPdfExportSliceUpload)
	whiteboard.Post("/pdf-export/merge", r.ctrl.FileController.HandleWhiteboardPdfExportMerge)

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

func (r *Router) registerInsightsRegisterAPIRoutes(api fiber.Router) {
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
