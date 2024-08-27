package routers

import (
	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	rr "github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/analytics"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/bbb"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/bkroom"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/common"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/etherpad"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/exdisplay"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/exmedia"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/file"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/ingress"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/lti/v1"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/poll"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/recording"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/room"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/speechtotext"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/user"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/waitingroom"
	"github.com/mynaparrot/plugnmeet-server/pkg/controllers/webhook"
	"github.com/mynaparrot/plugnmeet-server/version"
)

func New() *fiber.App {
	templateEngine := html.New(config.GetConfig().Client.Path, ".html")

	if config.GetConfig().Client.Debug {
		templateEngine.Reload(true)
		templateEngine.Debug(true)
	}

	cnf := fiber.Config{
		JSONEncoder: json.Marshal,
		JSONDecoder: json.Unmarshal,
		Views:       templateEngine,
		AppName:     "plugNmeet version: " + version.Version,
	}

	if config.GetConfig().Client.ProxyHeader != "" {
		cnf.ProxyHeader = config.GetConfig().Client.ProxyHeader
	}

	app := fiber.New(cnf)

	if config.GetConfig().Client.Debug {
		app.Use(logger.New())
	}
	if config.GetConfig().Client.PrometheusConf.Enable {
		prometheus := fiberprometheus.New("plugNmeet")
		prometheus.RegisterAt(app, config.GetConfig().Client.PrometheusConf.MetricsPath)
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
	app.Post("/webhook", webhookcontroller.HandleWebhook)
	app.Get("/download/uploadedFile/:sid/*", filecontroller.HandleDownloadUploadedFile)
	app.Get("/download/recording/:token", recordingcontroller.HandleDownloadRecording)
	app.Get("/download/analytics/:token", analyticscontroller.HandleDownloadAnalytics)
	app.Get("/healthCheck", commoncontroller.HandleHealthCheck)

	// lti group
	lti := app.Group("/lti")
	lti.Get("/v1", ltiv1controller.HandleLTIV1GETREQUEST)
	lti.Post("/v1", ltiv1controller.HandleLTIV1Landing)
	ltiV1API := lti.Group("/v1/api", ltiv1controller.HandleLTIV1VerifyHeaderToken)
	ltiV1API.Post("/room/join", ltiv1controller.HandleLTIV1JoinRoom)
	ltiV1API.Post("/room/isActive", ltiv1controller.HandleLTIV1IsRoomActive)
	ltiV1API.Post("/room/end", ltiv1controller.HandleLTIV1EndRoom)
	ltiV1API.Post("/recording/fetch", ltiv1controller.HandleLTIV1FetchRecordings)
	ltiV1API.Post("/recording/download", ltiv1controller.HandleLTIV1GetRecordingDownloadToken)
	ltiV1API.Post("/recording/delete", ltiv1controller.HandleLTIV1DeleteRecordings)

	auth := app.Group("/auth", roomcontroller.HandleAuthHeaderCheck)
	auth.Post("/getClientFiles", filecontroller.HandleGetClientFiles)

	// for room
	room := auth.Group("/room")
	room.Post("/create", roomcontroller.HandleRoomCreate)
	room.Post("/getJoinToken", roomcontroller.HandleGenerateJoinToken)
	room.Post("/isRoomActive", roomcontroller.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", roomcontroller.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", roomcontroller.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", roomcontroller.HandleEndRoom)
	room.Post("/fetchPastRooms", roomcontroller.HandleFetchPastRooms)

	// for recording
	recording := auth.Group("/recording")
	recording.Post("/fetch", recordingcontroller.HandleFetchRecordings)
	recording.Post("/recordingInfo", recordingcontroller.HandleRecordingInfo)
	recording.Post("/delete", recordingcontroller.HandleDeleteRecording)
	recording.Post("/getDownloadToken", recordingcontroller.HandleGetDownloadToken)

	// for analytics
	analytics := auth.Group("/analytics")
	analytics.Post("/fetch", analyticscontroller.HandleFetchAnalytics)
	analytics.Post("/delete", analyticscontroller.HandleDeleteAnalytics)
	analytics.Post("/getDownloadToken", analyticscontroller.HandleGetAnalyticsDownloadToken)

	// to handle different events from recorder
	recorder := auth.Group("/recorder")
	recorder.Post("/notify", recordingcontroller.HandleRecorderEvents)

	// for convert BBB request to PlugNmeet
	bbb := app.Group("/:apiKey/bigbluebutton/api", bbbcontroller.HandleVerifyApiRequest)
	bbb.All("/create", bbbcontroller.HandleBBBCreate)
	bbb.All("/join", bbbcontroller.HandleBBBJoin)
	bbb.All("/isMeetingRunning", bbbcontroller.HandleBBBIsMeetingRunning)
	bbb.All("/getMeetingInfo", bbbcontroller.HandleBBBGetMeetingInfo)
	bbb.All("/getMeetings", bbbcontroller.HandleBBBGetMeetings)
	bbb.All("/end", bbbcontroller.HandleBBBEndMeetings)
	bbb.All("/getRecordings", bbbcontroller.HandleBBBGetRecordings)
	bbb.All("/deleteRecordings", bbbcontroller.HandleBBBDeleteRecordings)
	// TO-DO: in the future
	bbb.All("/updateRecordings", bbbcontroller.HandleBBBUpdateRecordings)
	bbb.All("/publishRecordings", bbbcontroller.HandleBBBPublishRecordings)

	// api group will require sending token as Authorization header value
	api := app.Group("/api", roomcontroller.HandleVerifyHeaderToken)
	api.Post("/verifyToken", roomcontroller.HandleVerifyToken)

	api.Post("/recording", recordingcontroller.HandleRecording)
	api.Post("/rtmp", recordingcontroller.HandleRTMP)
	api.Post("/endRoom", roomcontroller.HandleEndRoomForAPI)
	api.Post("/changeVisibility", roomcontroller.HandleChangeVisibilityForAPI)
	api.Post("/convertWhiteboardFile", filecontroller.HandleConvertWhiteboardFile)
	api.Post("/externalMediaPlayer", exmediacontroller.HandleExternalMediaPlayer)
	api.Post("/externalDisplayLink", exdisplaycontroller.HandleExternalDisplayLink)

	api.Post("/updateLockSettings", usercontroller.HandleUpdateUserLockSetting)
	api.Post("/muteUnmuteTrack", usercontroller.HandleMuteUnMuteTrack)
	api.Post("/removeParticipant", usercontroller.HandleRemoveParticipant)
	api.Post("/switchPresenter", usercontroller.HandleSwitchPresenter)

	// etherpad group
	etherpad := api.Group("/etherpad")
	etherpad.Post("/create", etherpadcontroller.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", etherpadcontroller.HandleCleanPad)
	etherpad.Post("/changeStatus", etherpadcontroller.HandleChangeEtherpadStatus)

	// waiting room group
	waitingRoom := api.Group("/waitingRoom")
	waitingRoom.Post("/approveUsers", waitingroomcontroller.HandleApproveUsers)
	waitingRoom.Post("/updateMsg", waitingroomcontroller.HandleUpdateWaitingRoomMessage)

	// polls group
	polls := api.Group("/polls")
	polls.Post("/create", pollcontroller.HandleCreatePoll)
	polls.Get("/listPolls", pollcontroller.HandleListPolls)
	polls.Get("/pollsStats", pollcontroller.HandleGetPollsStats)
	polls.Get("/countTotalResponses/:pollId", pollcontroller.HandleCountPollTotalResponses)
	polls.Get("/userSelectedOption/:pollId/:userId", pollcontroller.HandleUserSelectedOption)
	polls.Get("/pollResponsesDetails/:pollId", pollcontroller.HandleGetPollResponsesDetails)
	polls.Get("/pollResponsesResult/:pollId", pollcontroller.HandleGetResponsesResult)
	polls.Post("/submitResponse", pollcontroller.HandleUserSubmitResponse)
	polls.Post("/closePoll", pollcontroller.HandleClosePoll)

	// breakout room group
	breakoutRoom := api.Group("/breakoutRoom")
	breakoutRoom.Post("/create", bkroomcontroller.HandleCreateBreakoutRooms)
	breakoutRoom.Post("/join", bkroomcontroller.HandleJoinBreakoutRoom)
	breakoutRoom.Get("/listRooms", bkroomcontroller.HandleGetBreakoutRooms)
	breakoutRoom.Get("/myRooms", bkroomcontroller.HandleGetMyBreakoutRooms)
	breakoutRoom.Post("/increaseDuration", bkroomcontroller.HandleIncreaseBreakoutRoomDuration)
	breakoutRoom.Post("/sendMsg", bkroomcontroller.HandleSendBreakoutRoomMsg)
	breakoutRoom.Post("/endRoom", bkroomcontroller.HandleEndBreakoutRoom)
	breakoutRoom.Post("/endAllRooms", bkroomcontroller.HandleEndBreakoutRooms)

	// Ingress
	ingress := api.Group("/ingress")
	ingress.Post("/create", ingresscontroller.HandleCreateIngress)

	// Speech services
	speech := api.Group("/speechServices")
	speech.Post("/serviceStatus", speechtotextcontroller.HandleSpeechToTextTranslationServiceStatus)
	speech.Post("/azureToken", speechtotextcontroller.HandleGenerateAzureToken)
	speech.Post("/userStatus", speechtotextcontroller.HandleSpeechServiceUserStatus)
	speech.Post("/renewToken", speechtotextcontroller.HandleRenewAzureToken)

	// for resumable.js need both methods.
	// https://github.com/23/resumable.js#how-do-i-set-it-up-with-my-server
	api.Get("/fileUpload", filecontroller.HandleFileUpload)
	api.Post("/fileUpload", filecontroller.HandleFileUpload)

	// last method
	app.Use(func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("not found")
	})

	return app
}
