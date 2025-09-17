package livekitservice

import (
	"context"

	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

type LivekitService struct {
	app    *config.AppConfig
	ctx    context.Context
	lkc    *lksdk.RoomServiceClient
	logger *logrus.Entry
}

func New(app *config.AppConfig, logger *logrus.Logger) *LivekitService {
	cnf := app.LivekitInfo
	lkc := lksdk.NewRoomServiceClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	return &LivekitService{
		ctx:    context.Background(),
		app:    app,
		lkc:    lkc,
		logger: logger.WithField("service", "livekit"),
	}
}
