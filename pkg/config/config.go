package config

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/logging"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var dbTablePrefix string

type AppConfig struct {
	ctx         context.Context
	RDS         *redis.Client
	DB          *gorm.DB
	Logger      *logrus.Logger
	NatsConn    *nats.Conn
	JetStream   jetstream.JetStream
	ClientFiles map[string][]string

	RootWorkingDir      string
	Client              ClientInfo                 `yaml:"client"`
	RoomDefaultSettings *utils.RoomDefaultSettings `yaml:"room_default_settings"`
	LogSettings         logging.LogSettings        `yaml:"log_settings"`
	LivekitInfo         LivekitInfo                `yaml:"livekit_info"`
	RedisInfo           RedisInfo                  `yaml:"redis_info"`
	DatabaseInfo        DatabaseInfo               `yaml:"database_info"`
	UploadFileSettings  UploadFileSettings         `yaml:"upload_file_settings"`
	RecorderInfo        RecorderInfo               `yaml:"recorder_info"`
	SharedNotePad       SharedNotePad              `yaml:"shared_notepad"`
	//deprecated: use insights features
	AzureCognitiveServicesSpeech AzureCognitiveServicesSpeech `yaml:"azure_cognitive_services_speech"`
	AnalyticsSettings            *AnalyticsSettings           `yaml:"analytics_settings"`
	ArtifactsSettings            *ArtifactsSettings           `yaml:"artifacts_settings"`
	NatsInfo                     NatsInfo                     `yaml:"nats_info"`
	Insights                     *InsightsConfig              `yaml:"insights"`
}

type ClientInfo struct {
	Port           int            `yaml:"port"`
	Debug          bool           `yaml:"debug"`
	Path           string         `yaml:"path"`
	AssetHost      *string        `yaml:"asset_host"`
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
	Enabled bool `yaml:"enabled"`
	// Deprecated: Use ArtifactsSettings instead.
	FilesStorePath *string `yaml:"files_store_path"`
}

type ArtifactsSettings struct {
	StoragePath                *string        `yaml:"storage_path"`
	TokenValidity              *time.Duration `yaml:"token_validity"`
	EnableDelArtifactsBackup   bool           `yaml:"enable_del_artifacts_backup"`
	DelArtifactsBackupPath     string         `yaml:"del_artifacts_backup_path"`
	DelArtifactsBackupDuration time.Duration  `yaml:"del_artifacts_backup_duration"`
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
	DriverName      string          `yaml:"driver_name"`
	Host            string          `yaml:"host"`
	Port            int32           `yaml:"port"`
	Username        string          `yaml:"username"`
	Password        string          `yaml:"password"`
	DBName          string          `yaml:"db"`
	Prefix          string          `yaml:"prefix"`
	Charset         *string         `yaml:"charset"`
	Loc             *string         `yaml:"loc"`
	ConnMaxLifetime *time.Duration  `yaml:"conn_max_lifetime"`
	MaxOpenConns    *int            `yaml:"max_open_conns"`
	Replicas        []ReplicaDBInfo `yaml:"replicas"`
}

// ReplicaDBInfo holds connection details for a read replica database.
type ReplicaDBInfo struct {
	Host     string `yaml:"host"`
	Port     int32  `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
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
	TranscodingJobs string `yaml:"transcoding_jobs_subject"`
}

func New(ctx context.Context, appCnf *AppConfig) (*AppConfig, error) {
	// default validation of token is 10 minutes
	if appCnf.Client.TokenValidity == nil || *appCnf.Client.TokenValidity < 0 {
		validity := time.Minute * 10
		appCnf.Client.TokenValidity = &validity
	}
	appCnf.ctx = ctx

	// set default values
	if appCnf.AnalyticsSettings != nil {
		//TODO: deprecated, will remove in future
		if appCnf.AnalyticsSettings.FilesStorePath == nil {
			p := "./analytics"
			appCnf.AnalyticsSettings.FilesStorePath = &p
		}
	}

	if strings.HasPrefix(appCnf.LogSettings.LogFile, "./") {
		appCnf.LogSettings.LogFile = filepath.Join(appCnf.RootWorkingDir, appCnf.LogSettings.LogFile)
	}
	if strings.HasPrefix(appCnf.UploadFileSettings.Path, "./") {
		appCnf.UploadFileSettings.Path = filepath.Join(appCnf.RootWorkingDir, appCnf.UploadFileSettings.Path)
	}
	if strings.HasPrefix(appCnf.RecorderInfo.RecordingFilesPath, "./") {
		appCnf.RecorderInfo.RecordingFilesPath = filepath.Join(appCnf.RootWorkingDir, appCnf.RecorderInfo.RecordingFilesPath)
		appCnf.RecorderInfo.DelRecordingBackupPath = filepath.Join(appCnf.RootWorkingDir, appCnf.RecorderInfo.DelRecordingBackupPath)
	}

	// setup everything for artifacts
	err := handleArtifactsSettings(appCnf)
	if err != nil {
		return nil, err
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
			return nil, fmt.Errorf("failed to create recording backup directory %s: %w", appCnf.RecorderInfo.DelRecordingBackupPath, err)
		}
	}

	if appCnf.DatabaseInfo.Prefix != "" {
		dbTablePrefix = appCnf.DatabaseInfo.Prefix
	}
	if appCnf.NatsInfo.Recorder.TranscodingJobs == "" {
		appCnf.NatsInfo.Recorder.TranscodingJobs = "pnm-RecorderTranscoderJobs"
	}

	// read client files and cache it
	err = readClientFiles(appCnf)
	if err != nil {
		return nil, err
	}

	return appCnf, nil
}

func handleArtifactsSettings(appCnf *AppConfig) error {
	// Add initialization logic for ArtifactsSettings
	if appCnf.ArtifactsSettings == nil {
		// If the whole block is missing, create it
		appCnf.ArtifactsSettings = &ArtifactsSettings{
			EnableDelArtifactsBackup: true,
		}
	}
	if appCnf.ArtifactsSettings.StoragePath == nil {
		// Set the default path if it's not specified
		p := "./artifacts"
		appCnf.ArtifactsSettings.StoragePath = &p
	}
	if appCnf.ArtifactsSettings.TokenValidity == nil {
		d := time.Minute * 10
		appCnf.ArtifactsSettings.TokenValidity = &d
	}

	p := *appCnf.ArtifactsSettings.StoragePath
	if strings.HasPrefix(p, "./") {
		p = filepath.Join(appCnf.RootWorkingDir, p)
	}

	if _, err := os.Stat(p); os.IsNotExist(err) {
		err = os.MkdirAll(p, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to create artifacts directory %s: %w", p, err)
		}
	}

	// Add new logic for the backup path
	if appCnf.ArtifactsSettings.EnableDelArtifactsBackup {
		if appCnf.ArtifactsSettings.DelArtifactsBackupDuration == 0 {
			appCnf.ArtifactsSettings.DelArtifactsBackupDuration = time.Hour * 72
		}
		if appCnf.ArtifactsSettings.DelArtifactsBackupPath == "" {
			// Default to a "trash" subdirectory inside the main storage path
			appCnf.ArtifactsSettings.DelArtifactsBackupPath = filepath.Join(*appCnf.ArtifactsSettings.StoragePath, "trash")
		}

		trashPath := appCnf.ArtifactsSettings.DelArtifactsBackupPath
		if strings.HasPrefix(trashPath, "./") {
			trashPath = filepath.Join(appCnf.RootWorkingDir, trashPath)
		}
		err := os.MkdirAll(trashPath, 0755)
		if err != nil {
			return fmt.Errorf("failed to create artifacts backup directory %s: %w", trashPath, err)
		}
	}
	return nil
}

func (a *AppConfig) GetApplicationCtx() context.Context {
	return a.ctx
}

// readClientFiles will read client files and cache it at startup
func readClientFiles(a *AppConfig) error {
	a.ClientFiles = make(map[string][]string)

	// if enable debug mode, then we won't cache files
	// otherwise changes of files won't be loaded
	if a.Client.Debug {
		return nil
	}

	if strings.HasPrefix(a.Client.Path, "./") {
		a.Client.Path = filepath.Join(a.RootWorkingDir, a.Client.Path)
	}

	cssPath := filepath.Join(a.Client.Path, "assets", "css")
	css, err := utils.GetFilesFromDir(cssPath, ".css", "des")
	if err != nil {
		logrus.WithError(err).Errorf("failed to read css files from %s", cssPath)
		return err
	}

	jsPath := filepath.Join(a.Client.Path, "assets", "js")
	js, err := utils.GetFilesFromDir(jsPath, ".js", "asc")
	if err != nil {
		logrus.WithError(err).Errorf("failed to read js files from %s", jsPath)
		return err
	}

	a.ClientFiles["css"] = css
	a.ClientFiles["js"] = js
	return nil
}

func FormatDBTable(table string) string {
	if dbTablePrefix != "" {
		return dbTablePrefix + table
	}
	return table
}
