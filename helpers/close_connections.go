package helpers

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func HandleCloseConnections() {
	if config.GetConfig() == nil {
		return
	}

	// handle to close DB connection
	if db, err := config.GetConfig().DB.DB(); err == nil {
		_ = db.Close()
	}

	// close redis
	_ = config.GetConfig().RDS.Close()

	// close nats
	natsservice.GetNatsCacheService().Shutdown()
	_ = config.GetConfig().NatsConn.Drain()
	config.GetConfig().NatsConn.Close()

	// close logger
	logrus.Exit(0)
}
