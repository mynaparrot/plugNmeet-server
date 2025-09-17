package helpers

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/sirupsen/logrus"
)

func HandleCloseConnections(appCnf *config.AppConfig) {
	if appCnf == nil {
		return
	}

	// handle to close DB connection
	if db, err := appCnf.DB.DB(); err == nil {
		_ = db.Close()
	}

	// close redis
	_ = appCnf.RDS.Close()

	// close nats
	natsservice.GetNatsCacheService(appCnf, appCnf.Logger).Shutdown()
	_ = appCnf.NatsConn.Drain()
	appCnf.NatsConn.Close()

	// close logger
	logrus.Exit(0)
}
