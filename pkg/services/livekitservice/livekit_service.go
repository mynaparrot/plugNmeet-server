package livekitservice

import (
	"context"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type LivekitService struct {
	ctx context.Context
	lkc *lksdk.RoomServiceClient
	rs  *redisservice.RedisService
}

func NewLivekitService(rs *redisservice.RedisService) *LivekitService {
	cnf := config.GetConfig().LivekitInfo
	lkc := lksdk.NewRoomServiceClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	return &LivekitService{
		ctx: context.Background(),
		lkc: lkc,
		rs:  rs,
	}
}
