package handler

import (
	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/goccy/go-json"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	rr "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers"
	"github.com/mynaparrot/plugnmeet-server/version"
	log "github.com/sirupsen/logrus"
)

func Router() *fiber.App {
	// call recovery if panic happens
	defer func() {
		if r := recover(); r != nil {
			log.Error(r)
		}
	}()

	templateEngine := html.New(config.AppCnf.Client.Path, ".html")

	if config.AppCnf.Client.Debug {
		templateEngine.Reload(true)
		templateEngine.Debug(true)
	}

	cnf := fiber.Config{
		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,
		Views:       templateEngine,
		AppName:     "plugNmeet version: " + version.Version,
	}

	if config.AppCnf.Client.ProxyHeader != "" {
		cnf.ProxyHeader = config.AppCnf.Client.ProxyHeader
	}

	app := fiber.New(cnf)

	if config.AppCnf.Client.Debug {
		app.Use(logger.New())
	}
	if config.AppCnf.Client.PrometheusConf.Enable {
		prometheus := fiberprometheus.New("plugNmeet")
		prometheus.RegisterAt(app, config.AppCnf.Client.PrometheusConf.MetricsPath)
		app.Use(prometheus.Middleware)
	}
	app.Use(rr.New())
	app.Use(cors.New(cors.Config{
		AllowMethods: "POST,GET,OPTIONS",
	}))

	app.Static("/assets", config.AppCnf.Client.Path+"/assets")
	app.Static("/favicon.ico", config.AppCnf.Client.Path+"/assets/imgs/favicon.ico")

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", nil)
	})
	app.Get("/login*", func(c *fiber.Ctx) error {
		return c.Render("login", nil)
	})
	app.Post("/webhook", controllers.HandleWebhook)
	app.Get("/download/uploadedFile/:sid/*", controllers.HandleDownloadUploadedFile)
	app.Get("/download/recording/:token", controllers.HandleDownloadRecording)
	app.Get("/download/analytics/:token", controllers.HandleDownloadAnalytics)
	app.Get("/healthCheck", controllers.HandleHealthCheck)

	// lti group
	lti := app.Group("/lti")
	lti.Get("/v1", controllers.HandleLTIV1GETREQUEST)
	lti.Post("/v1", controllers.HandleLTIV1Landing)
	ltiV1API := lti.Group("/v1/api", controllers.HandleLTIV1VerifyHeaderToken)
	ltiV1API.Post("/room/join", controllers.HandleLTIV1JoinRoom)
	ltiV1API.Post("/room/isActive", controllers.HandleLTIV1IsRoomActive)
	ltiV1API.Post("/room/end", controllers.HandleLTIV1EndRoom)
	ltiV1API.Post("/recording/fetch", controllers.HandleLTIV1FetchRecordings)
	ltiV1API.Post("/recording/download", controllers.HandleLTIV1GetRecordingDownloadToken)
	ltiV1API.Post("/recording/delete", controllers.HandleLTIV1DeleteRecordings)

	auth := app.Group("/auth", controllers.HandleAuthHeaderCheck)
	auth.Post("/getClientFiles", controllers.HandleGetClientFiles)

	// for room
	room := auth.Group("/room")
	room.Post("/create", controllers.HandleRoomCreate)
	room.Post("/getJoinToken", controllers.HandleGenerateJoinToken)
	room.Post("/isRoomActive", controllers.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", controllers.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", controllers.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", controllers.HandleEndRoom)
	room.Post("/fetchPastRooms", controllers.HandleFetchPastRooms)

	// for recording
	recording := auth.Group("/recording")
	recording.Post("/fetch", controllers.HandleFetchRecordings)
	recording.Post("/recordingInfo", controllers.HandleRecordingInfo)
	recording.Post("/delete", controllers.HandleDeleteRecording)
	recording.Post("/getDownloadToken", controllers.HandleGetDownloadToken)

	// for analytics
	analytics := auth.Group("/analytics")
	analytics.Post("/fetch", controllers.HandleFetchAnalytics)
	analytics.Post("/delete", controllers.HandleDeleteAnalytics)
	analytics.Post("/getDownloadToken", controllers.HandleGetAnalyticsDownloadToken)

	// to handle different events from recorder
	recorder := auth.Group("/recorder")
	recorder.Post("/notify", controllers.HandleRecorderEvents)

	// for convert BBB request to PlugNmeet
	bbb := app.Group("/:apiKey/bigbluebutton/api", controllers.HandleVerifyApiRequest)
	bbb.All("/create", controllers.HandleBBBCreate)
	bbb.All("/join", controllers.HandleBBBJoin)
	bbb.All("/isMeetingRunning", controllers.HandleBBBIsMeetingRunning)
	bbb.All("/getMeetingInfo", controllers.HandleBBBGetMeetingInfo)
	bbb.All("/getMeetings", controllers.HandleBBBGetMeetings)
	bbb.All("/end", controllers.HandleBBBEndMeetings)
	bbb.All("/getRecordings", controllers.HandleBBBGetRecordings)
	bbb.All("/deleteRecordings", controllers.HandleBBBDeleteRecordings)
	// TO-DO: in the future
	bbb.All("/updateRecordings", controllers.HandleBBBUpdateRecordings)
	bbb.All("/publishRecordings", controllers.HandleBBBPublishRecordings)

	// api group will require sending token as Authorization header value
	api := app.Group("/api", controllers.HandleVerifyHeaderToken)
	api.Post("/verifyToken", controllers.HandleVerifyToken)
	api.Post("/renewToken", controllers.HandleRenewToken)

	api.Post("/recording", controllers.HandleRecording)
	api.Post("/rtmp", controllers.HandleRTMP)
	api.Post("/updateLockSettings", controllers.HandleUpdateUserLockSetting)
	api.Post("/muteUnmuteTrack", controllers.HandleMuteUnMuteTrack)
	api.Post("/removeParticipant", controllers.HandleRemoveParticipant)
	api.Post("/dataMessage", controllers.HandleDataMessage)
	api.Post("/endRoom", controllers.HandleEndRoomForAPI)
	api.Post("/changeVisibility", controllers.HandleChangeVisibilityForAPI)
	api.Post("/convertWhiteboardFile", controllers.HandleConvertWhiteboardFile)
	api.Post("/externalMediaPlayer", controllers.HandleExternalMediaPlayer)
	api.Post("/switchPresenter", controllers.HandleSwitchPresenter)
	api.Post("/externalDisplayLink", controllers.HandleExternalDisplayLink)

	// etherpad group
	etherpad := api.Group("/etherpad")
	etherpad.Post("/create", controllers.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", controllers.HandleCleanPad)
	etherpad.Post("/changeStatus", controllers.HandleChangeEtherpadStatus)

	// waiting room group
	waitingRoom := api.Group("/waitingRoom")
	waitingRoom.Post("/approveUsers", controllers.HandleApproveUsers)
	waitingRoom.Post("/updateMsg", controllers.HandleUpdateWaitingRoomMessage)

	// polls group
	polls := api.Group("/polls")
	polls.Post("/create", controllers.HandleCreatePoll)
	polls.Get("/listPolls", controllers.HandleListPolls)
	polls.Get("/pollsStats", controllers.HandleGetPollsStats)
	polls.Get("/countTotalResponses/:pollId", controllers.HandleCountPollTotalResponses)
	polls.Get("/userSelectedOption/:pollId/:userId", controllers.HandleUserSelectedOption)
	polls.Get("/pollResponsesDetails/:pollId", controllers.HandleGetPollResponsesDetails)
	polls.Get("/pollResponsesResult/:pollId", controllers.HandleGetResponsesResult)
	polls.Post("/submitResponse", controllers.HandleUserSubmitResponse)
	polls.Post("/closePoll", controllers.HandleClosePoll)

	// breakout room group
	breakoutRoom := api.Group("/breakoutRoom")
	breakoutRoom.Post("/create", controllers.HandleCreateBreakoutRooms)
	breakoutRoom.Post("/join", controllers.HandleJoinBreakoutRoom)
	breakoutRoom.Get("/listRooms", controllers.HandleGetBreakoutRooms)
	breakoutRoom.Get("/myRooms", controllers.HandleGetMyBreakoutRooms)
	breakoutRoom.Post("/increaseDuration", controllers.HandleIncreaseBreakoutRoomDuration)
	breakoutRoom.Post("/sendMsg", controllers.HandleSendBreakoutRoomMsg)
	breakoutRoom.Post("/endRoom", controllers.HandleEndBreakoutRoom)
	breakoutRoom.Post("/endAllRooms", controllers.HandleEndBreakoutRooms)

	// Ingress
	ingress := api.Group("/ingress")
	ingress.Post("/create", controllers.HandleCreateIngress)

	// Speech services
	speech := api.Group("/speechServices")
	speech.Post("/serviceStatus", controllers.HandleSpeechToTextTranslationServiceStatus)
	speech.Post("/azureToken", controllers.HandleGenerateAzureToken)
	speech.Post("/userStatus", controllers.HandleSpeechServiceUserStatus)
	speech.Post("/renewToken", controllers.HandleRenewAzureToken)

	// for resumable.js need both methods.
	// https://github.com/23/resumable.js#how-do-i-set-it-up-with-my-server
	api.Get("/fileUpload", controllers.HandleFileUpload)
	api.Post("/fileUpload", controllers.HandleFileUpload)

	// websocket for chat
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			c.Locals("allowed", true)
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})
	controllers.SetupSocketListeners()
	app.Get("/ws", controllers.HandleWebSocket())

	// last method
	app.Use(func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("not found")
	})

	return app
}
