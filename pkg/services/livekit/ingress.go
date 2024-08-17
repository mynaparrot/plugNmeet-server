package livekitservice

import (
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

func (s *LivekitService) CreateIngress(req *livekit.CreateIngressRequest) (*livekit.IngressInfo, error) {
	cnf := s.app.LivekitInfo
	ic := lksdk.NewIngressClient(cnf.Host, cnf.ApiKey, cnf.Secret)

	return ic.CreateIngress(s.ctx, req)
}
