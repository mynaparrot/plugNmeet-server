package livekitservice

import (
	"context"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type LivekitService struct {
	app *config.AppConfig
	ctx context.Context
	lkc *lksdk.RoomServiceClient
	rs  *redisservice.RedisService
}

func New(app *config.AppConfig, rs *redisservice.RedisService) *LivekitService {
	cnf := app.LivekitInfo
	lkc := lksdk.NewRoomServiceClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	return &LivekitService{
		ctx: context.Background(),
		app: app,
		lkc: lkc,
		rs:  rs,
	}
}
