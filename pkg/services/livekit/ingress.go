package livekitservice

import (
	"context"
	"time"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func (s *LivekitService) CreateIngress(req *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	cnf := s.app.LivekitInfo
	ic := lksdk.NewIngressClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	ctx, cancel := context.WithTimeout(s.ctx, time.Second*15)
	defer cancel()

	return ic.CreateIngress(ctx, req)
}
