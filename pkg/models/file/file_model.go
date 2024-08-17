package filemodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
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
		ds = dbservice.New(app.ORM)
	}
	if rs == nil {
		rs = redisservice.New(app.RDS)
	}
	if lk == nil {
		lk = livekitservice.New(app, rs)
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
