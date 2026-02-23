package factory

import (
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/sirupsen/logrus"
)

func NewNatsConnection(appCnf *config.AppConfig) error {
	info := appCnf.NatsInfo
	var err error
	opts := []nats.Option{
		nats.Name("plugnmeet-server"),
	}

	if info.Nkey != nil {
		opt, err := utils.NkeyOptionFromSeedText(*info.Nkey)
		if err != nil {
			return err
		}
		opts = append(opts, opt)
	} else {
		opt := nats.UserInfo(info.User, info.Password)
		opts = append(opts, opt)
	}

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), opts...)
	if err != nil {
		return err
	}
	appCnf.NatsConn = nc

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	appCnf.Logger.WithFields(logrus.Fields{
		"version": nc.ConnectedServerVersion(),
		"address": nc.ConnectedAddr(),
	}).Info("successfully connected to NATS server")
	appCnf.JetStream = js

	return nil
}
