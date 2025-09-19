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

func New(ctx context.Context, app *config.AppConfig, logger *logrus.Logger) *LivekitService {
	lkc := lksdk.NewRoomServiceClient(app.LivekitInfo.Host, app.LivekitInfo.ApiKey, app.LivekitInfo.Secret)

	return &LivekitService{
		ctx:    ctx,
		app:    app,
		lkc:    lkc,
		logger: logger.WithField("service", "livekit"),
	}
}
