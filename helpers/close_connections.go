package helpers

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

func HandleCloseConnections() {
	if config.GetConfig() == nil {
		return
	}

	// handle to close DB connection
	db, err := config.GetConfig().ORM.DB()
	if err == nil {
		_ = db.Close()
	}

	// close redis
	_ = config.GetConfig().RDS.Close()

	// close logger
	logrus.Exit(0)

	return
}
