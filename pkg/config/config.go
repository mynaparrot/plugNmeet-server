package config

import (
	"github.com/mynaparrot/plugnmeet-protocol/factory"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"gorm.io/gorm"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AppConfig struct {
	RDS       *redis.Client
	ORM       *gorm.DB
	NatsConn  *nats.Conn
	JetStream jetstream.JetStream

	RootWorkingDir string

	sync.RWMutex
	chatRooms   map[string]map[string]ChatParticipant
	ClientFiles map[string][]string

	Client                       ClientInfo                   `yaml:"client"`
	RoomDefaultSettings          *utils.RoomDefaultSettings   `yaml:"room_default_settings"`
	LogSettings                  LogSettings                  `yaml:"log_settings"`
	LivekitInfo                  LivekitInfo                  `yaml:"livekit_info"`
	RedisInfo                    *factory.RedisInfo           `yaml:"redis_info"`
	DatabaseInfo                 *factory.DatabaseInfo        `yaml:"database_info"`
	UploadFileSettings           UploadFileSettings           `yaml:"upload_file_settings"`
	RecorderInfo                 RecorderInfo                 `yaml:"recorder_info"`
	SharedNotePad                SharedNotePad                `yaml:"shared_notepad"`
	AzureCognitiveServicesSpeech AzureCognitiveServicesSpeech `yaml:"azure_cognitive_services_speech"`
	AnalyticsSettings            *AnalyticsSettings           `yaml:"analytics_settings"`
	NatsInfo                     NatsInfo                     `yaml:"nats_info"`
}

type ClientInfo struct {
	Port           int            `yaml:"port"`
	Debug          bool           `yaml:"debug"`
	Path           string         `yaml:"path"`
	ApiKey         string         `yaml:"api_key"`
	Secret         string         `yaml:"secret"`
	WebhookConf    WebhookConf    `yaml:"webhook_conf"`
	PrometheusConf PrometheusConf `yaml:"prometheus"`
	ProxyHeader    string         `yaml:"proxy_header"`
	CopyrightConf  *CopyrightConf `yaml:"copyright_conf"`
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
	Id           string `yaml:"id"`
	Host         string `yaml:"host"`
	ClientId     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
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

type CopyrightConf struct {
	Display       bool   `yaml:"display"`
	AllowOverride bool   `yaml:"allow_override"`
	Text          string `yaml:"text"`
}

type NatsInfo struct {
	NatsUrl                  string       `yaml:"nats_url"`
	Account                  string       `yaml:"account"`
	User                     string       `yaml:"user"`
	Password                 string       `yaml:"password"`
	AuthCalloutIssuerPrivate string       `yaml:"auth_callout_issuer_private"`
	Subjects                 NatsSubjects `yaml:"subjects"`
}

type NatsSubjects struct {
	SystemWorker  string `yaml:"system_worker"`
	SystemPublic  string `yaml:"system_public"`
	SystemPrivate string `yaml:"system_private"`
	Chat          string `yaml:"chat"`
	Whiteboard    string `yaml:"whiteboard"`
	DataChannel   string `yaml:"data_channel"`
}

var appCnf *AppConfig

func New(a *AppConfig) {
	if appCnf != nil {
		// not allow multiple config
		return
	}

	appCnf = new(AppConfig) // otherwise will give error
	// now set the config
	appCnf = a
	appCnf.chatRooms = make(map[string]map[string]ChatParticipant)

	// set default values
	if appCnf.AnalyticsSettings != nil {
		if appCnf.AnalyticsSettings.FilesStorePath == nil {
			p := "./analytics"
			appCnf.AnalyticsSettings.FilesStorePath = &p
			d := time.Minute * 30
			appCnf.AnalyticsSettings.TokenValidity = &d
		}

		p := *appCnf.AnalyticsSettings.FilesStorePath
		if strings.HasPrefix(p, "./") {
			p = filepath.Join(a.RootWorkingDir, p)
		}

		if _, err := os.Stat(p); os.IsNotExist(err) {
			_ = os.MkdirAll(p, os.ModePerm)
		}
	}

	setLogger()
	a.readClientFiles()
}

func GetConfig() *AppConfig {
	return appCnf
}

func setLogger() {
	p := appCnf.LogSettings.LogFile
	if strings.HasPrefix(p, "./") {
		p = filepath.Join(appCnf.RootWorkingDir, p)
	}

	logWriter := &lumberjack.Logger{
		Filename:   p,
		MaxSize:    appCnf.LogSettings.MaxSize,
		MaxBackups: appCnf.LogSettings.MaxBackups,
		MaxAge:     appCnf.LogSettings.MaxAge,
	}

	logrus.SetReportCaller(true)
	logrus.SetFormatter(&logrus.JSONFormatter{
		PrettyPrint: true,
	})
	logrus.RegisterExitHandler(func() {
		_ = logWriter.Close()
	})

	var w io.Writer
	if appCnf.Client.Debug {
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
	if a.DatabaseInfo.Prefix != "" {
		return a.DatabaseInfo.Prefix + table
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
	// if enable debug mode, then we won't cache files
	// otherwise changes of files won't be loaded
	if a.Client.Debug {
		return
	}
	appCnf.ClientFiles = make(map[string][]string)

	css, err := utils.GetFilesFromDir(a.Client.Path+"/assets/css", ".css", "des")
	if err != nil {
		logrus.Errorln(err)
	}

	js, err := utils.GetFilesFromDir(a.Client.Path+"/assets/js", ".js", "asc")
	if err != nil {
		logrus.Errorln(err)
	}

	appCnf.ClientFiles["css"] = css
	appCnf.ClientFiles["js"] = js
}
