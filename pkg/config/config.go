package config

import (
	"database/sql"
	"github.com/mynaparrot/plugnmeet-protocol/factory"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
	"sync"
	"time"
)

var AppCnf *AppConfig

type AppConfig struct {
	DB  *sql.DB
	RDS *redis.Client

	sync.RWMutex
	chatRooms   map[string]map[string]ChatParticipant
	ClientFiles map[string][]string

	Client                       ClientInfo                   `yaml:"client"`
	RoomDefaultSettings          *utils.RoomDefaultSettings   `yaml:"room_default_settings"`
	LogSettings                  LogSettings                  `yaml:"log_settings"`
	LivekitInfo                  LivekitInfo                  `yaml:"livekit_info"`
	RedisInfo                    *factory.RedisInfo           `yaml:"redis_info"`
	MySqlInfo                    *factory.MySqlInfo           `yaml:"mysql_info"`
	UploadFileSettings           UploadFileSettings           `yaml:"upload_file_settings"`
	RecorderInfo                 RecorderInfo                 `yaml:"recorder_info"`
	SharedNotePad                SharedNotePad                `yaml:"shared_notepad"`
	AzureCognitiveServicesSpeech AzureCognitiveServicesSpeech `yaml:"azure_cognitive_services_speech"`
	AnalyticsSettings            *AnalyticsSettings           `yaml:"analytics_settings"`
}

type ClientInfo struct {
	Port           int                      `yaml:"port"`
	Debug          bool                     `yaml:"debug"`
	Path           string                   `yaml:"path"`
	ApiKey         string                   `yaml:"api_key"`
	Secret         string                   `yaml:"secret"`
	WebhookConf    WebhookConf              `yaml:"webhook_conf"`
	PrometheusConf PrometheusConf           `yaml:"prometheus"`
	ProxyHeader    string                   `yaml:"proxy_header"`
	CopyrightConf  *plugnmeet.CopyrightConf `yaml:"copyright_conf"`
}

type WebhookConf struct {
	Enable              bool   `yaml:"enable"`
	Url                 string `yaml:"url,omitempty"`
	EnableForPerMeeting bool   `yaml:"enable_for_per_meeting"`
}

type PrometheusConf struct {
	Enable      bool   `yaml:"enable"`
	MetricsPath string `yaml:"metrics_path"`
}

type LogSettings struct {
	LogFile    string `yaml:"log_file"`
	MaxSize    int    `yaml:"max_size"`
	MaxBackups int    `yaml:"max_backups"`
	MaxAge     int    `yaml:"max_age"`
}

type LivekitInfo struct {
	Host          string        `yaml:"host"`
	ApiKey        string        `yaml:"api_key"`
	Secret        string        `yaml:"secret"`
	TokenValidity time.Duration `yaml:"token_validity"`
}

type UploadFileSettings struct {
	Path         string   `yaml:"path"`
	MaxSize      uint64   `yaml:"max_size"`
	KeepForever  bool     `yaml:"keep_forever"`
	AllowedTypes []string `yaml:"allowed_types"`
}

type RecorderInfo struct {
	RecordingFilesPath string        `yaml:"recording_files_path"`
	TokenValidity      time.Duration `yaml:"token_validity"`
}

type SharedNotePad struct {
	Enabled       bool           `yaml:"enabled"`
	EtherpadHosts []EtherpadInfo `yaml:"etherpad_hosts"`
}

type EtherpadInfo struct {
	Id     string `yaml:"id"`
	Host   string `yaml:"host"`
	ApiKey string `yaml:"api_key"`
}

type AzureCognitiveServicesSpeech struct {
	Enabled                       bool                   `yaml:"enabled"`
	MaxNumTranLangsAllowSelecting int32                  `yaml:"max_num_tran_langs"`
	SubscriptionKeys              []AzureSubscriptionKey `yaml:"subscription_keys"`
}

type AzureSubscriptionKey struct {
	Id              string `yaml:"id"`
	SubscriptionKey string `yaml:"subscription_key"`
	ServiceRegion   string `yaml:"service_region"`
	MaxConnection   int64  `yaml:"max_connection"`
}

type AnalyticsSettings struct {
	Enabled        bool           `yaml:"enabled"`
	FilesStorePath *string        `yaml:"files_store_path"`
	TokenValidity  *time.Duration `yaml:"token_validity"`
}

type ChatParticipant struct {
	RoomSid string
	RoomId  string
	Name    string
	UserSid string
	UserId  string
	UUID    string
	IsAdmin bool
}

func SetAppConfig(a *AppConfig) {
	AppCnf = a
	AppCnf.chatRooms = make(map[string]map[string]ChatParticipant)

	// set default values
	if AppCnf.AnalyticsSettings != nil {
		if AppCnf.AnalyticsSettings.FilesStorePath == nil {
			p := "./analytics"
			AppCnf.AnalyticsSettings.FilesStorePath = &p
			d := time.Minute * 30
			AppCnf.AnalyticsSettings.TokenValidity = &d
		}
		if _, err := os.Stat(*AppCnf.AnalyticsSettings.FilesStorePath); os.IsNotExist(err) {
			_ = os.MkdirAll(*AppCnf.AnalyticsSettings.FilesStorePath, os.ModePerm)
		}
	}

	setLogger()
	a.readClientFiles()
}

func setLogger() {
	logWriter := &lumberjack.Logger{
		Filename:   AppCnf.LogSettings.LogFile,
		MaxSize:    AppCnf.LogSettings.MaxSize,
		MaxBackups: AppCnf.LogSettings.MaxBackups,
		MaxAge:     AppCnf.LogSettings.MaxAge,
	}

	logrus.SetReportCaller(true)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.RegisterExitHandler(func() {
		_ = logWriter.Close()
	})

	var w io.Writer
	if AppCnf.Client.Debug {
		w = io.MultiWriter(os.Stdout, logWriter)
	} else {
		w = io.Writer(logWriter)
	}
	logrus.SetOutput(w)
}

func GetLogger() *logrus.Logger {
	return logrus.StandardLogger()
}

type ErrorResponse struct {
	FailedField string
	Tag         string
}

func (a *AppConfig) FormatDBTable(table string) string {
	if a.MySqlInfo.Prefix != "" {
		return a.MySqlInfo.Prefix + table
	}
	return table
}

func (a *AppConfig) AddChatUser(roomId string, participant ChatParticipant) {
	a.Lock()
	defer a.Unlock()

	if _, ok := a.chatRooms[roomId]; !ok {
		a.chatRooms[roomId] = make(map[string]ChatParticipant)
	}
	a.chatRooms[roomId][participant.UserId] = participant
}

func (a *AppConfig) GetChatParticipants(roomId string) map[string]ChatParticipant {
	// we don't need to lock implementation
	// as we'll require locking before looping over anyway

	return a.chatRooms[roomId]
}

func (a *AppConfig) RemoveChatParticipant(roomId, userId string) {
	a.Lock()
	defer a.Unlock()

	if _, ok := a.chatRooms[roomId]; ok {
		delete(a.chatRooms[roomId], userId)
	}
}

func (a *AppConfig) DeleteChatRoom(roomId string) {
	a.Lock()
	defer a.Unlock()

	if _, ok := a.chatRooms[roomId]; ok {
		delete(a.chatRooms, roomId)
	}
}

func (a *AppConfig) readClientFiles() {
	// if enable debug mode then we won't cache files
	// otherwise changes of files won't be load
	if a.Client.Debug {
		return
	}
	AppCnf.ClientFiles = make(map[string][]string)

	css, err := utils.GetFilesFromDir(a.Client.Path+"/assets/css", ".css", "des")
	if err != nil {
		logrus.Errorln(err)
	}

	js, err := utils.GetFilesFromDir(a.Client.Path+"/assets/js", ".js", "asc")
	if err != nil {
		logrus.Errorln(err)
	}

	AppCnf.ClientFiles["css"] = css
	AppCnf.ClientFiles["js"] = js
}
