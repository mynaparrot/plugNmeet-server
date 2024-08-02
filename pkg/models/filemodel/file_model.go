package filemodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekitservice"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redisservice"
)

type FileModel struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService

	req           *FileUploadReq
	fileExtension string
	fileMimeType  string
}

type FileUploadReq struct {
	Sid       string `json:"sid"`
	RoomId    string `json:"roomId"`
	UserId    string `json:"userId"`
	FilePath  string `json:"file_path"`
	Resumable bool   `json:"resumable"`
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *FileModel {
	if app == nil {
		app = config.GetConfig()
	}
	if ds == nil {
		ds = dbservice.NewDBService(app.ORM)
	}
	if rs == nil {
		rs = redisservice.NewRedisService(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.NewLivekitService(app, rs)
	}

	return &FileModel{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
	}
}

func (m *FileModel) AddRequest(req *FileUploadReq) {
	m.req = req
}
