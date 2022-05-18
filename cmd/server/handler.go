package main

import (
	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/template/html"
	"github.com/gofiber/websocket/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/controllers"
)

func Router() *fiber.App {
	templateEngine := html.New(config.AppCnf.Client.Path, ".html")

	if config.AppCnf.Client.Debug {
		templateEngine.Reload(true)
		templateEngine.Debug(true)
	}

	app := fiber.New(fiber.Config{
		Views: templateEngine,
	})
	app.Static("/assets", config.AppCnf.Client.Path+"/assets")
	app.Static("/favicon.ico", config.AppCnf.Client.Path+"/assets/imgs/favicon.ico")

	if config.AppCnf.Client.Debug {
		app.Use(logger.New())
	}
	if config.AppCnf.Client.PrometheusConf.Enable {
		prometheus := fiberprometheus.New("plugNmeet")
		prometheus.RegisterAt(app, config.AppCnf.Client.PrometheusConf.MetricsPath)
		app.Use(prometheus.Middleware)
	}
	app.Use(recover.New())
	app.Use(cors.New(cors.Config{
		AllowMethods: "POST,GET,OPTIONS",
	}))

	app.Get("/", func(c *fiber.Ctx) error {
		return c.Render("index", nil)
	})
	app.Get("/login*", func(c *fiber.Ctx) error {
		return c.Render("login", nil)
	})
	app.Post("/webhook", controllers.HandleWebhook)
	app.Get("/download/uploadedFile/:sid/*", controllers.HandleDownloadUploadedFile)
	app.Get("/download/recording/:token", controllers.HandleDownloadRecording)

	// auth group, will require API-KEY & API-SECRET as header value
	auth := app.Group("/auth", controllers.HandleAuthHeaderCheck)
	// for room
	room := auth.Group("/room")
	room.Post("/create", controllers.HandleRoomCreate)
	room.Post("/getJoinToken", controllers.HandleGenerateJoinToken)
	room.Post("/isRoomActive", controllers.HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", controllers.HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", controllers.HandleGetActiveRoomsInfo)
	room.Post("/endRoom", controllers.HandleEndRoom)
	// for recording
	recording := auth.Group("/recording")
	recording.Post("/fetch", controllers.HandleFetchRecordings)
	recording.Post("/delete", controllers.HandleDeleteRecording)
	recording.Post("/getDownloadToken", controllers.HandleGetDownloadToken)

	// to handle different events from recorder
	recorder := auth.Group("/recorder")
	recorder.Post("/notify", controllers.HandleRecorderEvents)

	// api group, will require sending token as Authorization header value
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
	api.Post("/approveWaitingUser", controllers.HandleApproveWaitingParticipant)

	// etherpad group
	etherpad := api.Group("/etherpad", controllers.HandleVerifyHeaderToken)
	etherpad.Post("/create", controllers.HandleCreateEtherpad)
	etherpad.Post("/cleanPad", controllers.HandleCleanPad)
	etherpad.Post("/changeStatus", controllers.HandleChangeEtherpadStatus)

	// for resumable.js need both methods.
	// https://github.com/23/resumable.js#how-do-i-set-it-up-with-my-server
	api.Get("/fileUpload", controllers.HandleChatFileUpload)
	api.Post("/fileUpload", controllers.HandleChatFileUpload)

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
		return c.Render("index", nil)
	})

	return app
}
