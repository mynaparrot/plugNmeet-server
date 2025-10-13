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
	var opt nats.Option
	var err error

	if info.Nkey != nil {
		opt, err = utils.NkeyOptionFromSeedText(*info.Nkey)
		if err != nil {
			return err
		}
	} else {
		opt = nats.UserInfo(info.User, info.Password)
	}

	nc, err := nats.Connect(strings.Join(info.NatsUrls, ","), opt)
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
