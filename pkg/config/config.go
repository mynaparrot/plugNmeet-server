package config

import (
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type AppConfig struct {
	RDS         *redis.Client
	DB          *gorm.DB
	Logger      *logrus.Logger
	NatsConn    *nats.Conn
	JetStream   jetstream.JetStream
	ClientFiles map[string][]string

	RootWorkingDir               string
	Client                       ClientInfo                   `yaml:"client"`
	RoomDefaultSettings          *utils.RoomDefaultSettings   `yaml:"room_default_settings"`
	LogSettings                  LogSettings                  `yaml:"log_settings"`
	LivekitInfo                  LivekitInfo                  `yaml:"livekit_info"`
	RedisInfo                    RedisInfo                    `yaml:"redis_info"`
	DatabaseInfo                 DatabaseInfo                 `yaml:"database_info"`
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
	TokenValidity  *time.Duration `yaml:"token_validity"`
	WebhookConf    WebhookConf    `yaml:"webhook_conf"`
	PrometheusConf PrometheusConf `yaml:"prometheus"`
	ProxyHeader    string         `yaml:"proxy_header"`
	CopyrightConf  *CopyrightConf `yaml:"copyright_conf"`
	BBBJoinHost    *string        `yaml:"bbb_join_host"`
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
	LogFile    string  `yaml:"log_file"`
	MaxSize    int     `yaml:"max_size"`
	MaxBackups int     `yaml:"max_backups"`
	MaxAge     int     `yaml:"max_age"`
	LogLevel   *string `yaml:"log_level"`
}

type LivekitInfo struct {
	Host   string `yaml:"host"`
	ApiKey string `yaml:"api_key"`
	Secret string `yaml:"secret"`
}

type UploadFileSettings struct {
	Path                  string   `yaml:"path"`
	MaxSize               uint64   `yaml:"max_size"`
	MaxSizeWhiteboardFile uint64   `yaml:"max_size_whiteboard_file"`
	KeepForever           bool     `yaml:"keep_forever"`
	AllowedTypes          []string `yaml:"allowed_types"`
}

type RecorderInfo struct {
	RecordingFilesPath         string        `yaml:"recording_files_path"`
	TokenValidity              time.Duration `yaml:"token_validity"`
	EnableDelRecordingBackup   bool          `yaml:"enable_del_recording_backup"`
	DelRecordingBackupPath     string        `yaml:"del_recording_backup_path"`
	DelRecordingBackupDuration time.Duration `yaml:"del_recording_backup_duration"`
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

type DatabaseInfo struct {
	DriverName      string         `yaml:"driver_name"`
	Host            string         `yaml:"host"`
	Port            int32          `yaml:"port"`
	Username        string         `yaml:"username"`
	Password        string         `yaml:"password"`
	DBName          string         `yaml:"db"`
	Prefix          string         `yaml:"prefix"`
	Charset         *string        `yaml:"charset"`
	Loc             *string        `yaml:"loc"`
	ConnMaxLifetime *time.Duration `yaml:"conn_max_lifetime"`
	MaxOpenConns    *int           `yaml:"max_open_conns"`
}

type RedisInfo struct {
	Host              string   `yaml:"host"`
	Username          string   `yaml:"username"`
	Password          string   `yaml:"password"`
	DBName            int      `yaml:"db"`
	UseTLS            bool     `yaml:"use_tls"`
	MasterName        string   `yaml:"sentinel_master_name"`
	SentinelUsername  string   `yaml:"sentinel_username"`
	SentinelPassword  string   `yaml:"sentinel_password"`
	SentinelAddresses []string `yaml:"sentinel_addresses"`
}

type NatsInfo struct {
	NatsUrls                 []string         `yaml:"nats_urls"`
	NatsWSUrls               []string         `yaml:"nats_ws_urls"`
	Account                  string           `yaml:"account"`
	User                     string           `yaml:"user"`
	Password                 string           `yaml:"password"`
	Nkey                     *string          `yaml:"nkey"`
	AuthCalloutIssuerPrivate string           `yaml:"auth_callout_issuer_private"`
	AuthCalloutXkeyPrivate   *string          `yaml:"auth_callout_xkey_private"`
	NumReplicas              int              `yaml:"num_replicas"`
	Subjects                 NatsSubjects     `yaml:"subjects"`
	Recorder                 NatsInfoRecorder `yaml:"recorder"`
}

type NatsSubjects struct {
	SystemApiWorker string `yaml:"system_api_worker"`
	SystemJsWorker  string `yaml:"system_js_worker"`
	SystemPublic    string `yaml:"system_public"`
	SystemPrivate   string `yaml:"system_private"`
	Chat            string `yaml:"chat"`
	Whiteboard      string `yaml:"whiteboard"`
	DataChannel     string `yaml:"data_channel"`
}

type NatsInfoRecorder struct {
	RecorderChannel string `yaml:"recorder_channel"`
	RecorderInfoKv  string `yaml:"recorder_info_kv"`
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

	// default validation of token is 10 minutes
	if appCnf.Client.TokenValidity == nil || *appCnf.Client.TokenValidity < 0 {
		validity := time.Minute * 10
		appCnf.Client.TokenValidity = &validity
	}

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

	// set default
	if appCnf.RecorderInfo.EnableDelRecordingBackup {
		if appCnf.RecorderInfo.DelRecordingBackupDuration == 0 {
			appCnf.RecorderInfo.DelRecordingBackupDuration = time.Hour * 72
		}

		if appCnf.RecorderInfo.DelRecordingBackupPath == "" {
			appCnf.RecorderInfo.DelRecordingBackupPath = path.Join(appCnf.RecorderInfo.RecordingFilesPath, "del_backup")
		}

		err := os.MkdirAll(appCnf.RecorderInfo.DelRecordingBackupPath, 0755)
		if err != nil {
			log.Fatal(err)
		}
	}

	a.readClientFiles()
}

func GetConfig() *AppConfig {
	return appCnf
}

func GetLogger() *logrus.Logger {
	return appCnf.Logger
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
