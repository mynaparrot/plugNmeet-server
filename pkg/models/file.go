package models

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type FileModel struct {
	app         *config.AppConfig
	ds          *dbservice.DatabaseService
	rs          *redisservice.RedisService
	natsService *natsservice.NatsService

	fileExtension string
	fileMimeType  string
}

type UploadedFileResponse struct {
	Status        bool   `json:"status"`
	Msg           string `json:"msg"`
	FilePath      string `json:"filePath"`
	FileName      string `json:"fileName"`
	FileExtension string `json:"fileExtension"`
	FileMimeType  string `json:"fileMimeType"`
}

func NewFileModel(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService) *FileModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.New(app.DB)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}

	return &FileModel{
		app:         app,
		ds:          ds,
		rs:          rs,
		natsService: natsservice.New(app),
	}
}
