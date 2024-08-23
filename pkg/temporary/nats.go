package temporary

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"strings"
)

func NewNatsConnection(appCnf *config.AppConfig) error {
	info := appCnf.NatsInfo

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), nats.UserInfo(info.User, info.Password))
	if err != nil {
		return err
	}
	appCnf.NatsConn = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	appCnf.JetStream = js

	return nil
}
