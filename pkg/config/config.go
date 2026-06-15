package config

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
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
	HookManager *hooks.HookProcessManager

	RootWorkingDir      string
	Client              ClientInfo                 `yaml:"client"`
	RoomDefaultSettings *utils.RoomDefaultSettings `yaml:"room_default_settings"`
	LogSettings         logging.LogSettings        `yaml:"log_settings"`
	LivekitInfo         LivekitInfo                `yaml:"livekit_info"`
	LivekitSipInfo      *LivekitSipInfo            `yaml:"livekit_sip_info"`
	RedisInfo           RedisInfo                  `yaml:"redis_info"`
	DatabaseInfo        DatabaseInfo               `yaml:"database_info"`
	UploadFileSettings  UploadFileSettings         `yaml:"upload_file_settings"`
	RecorderInfo        RecorderInfo               `yaml:"recorder_info"`
	SharedNotePad       SharedNotePad              `yaml:"shared_notepad"`
	AnalyticsSettings   *AnalyticsSettings         `yaml:"analytics_settings"`
	ArtifactsSettings   *ArtifactsSettings         `yaml:"artifacts_settings"`
	NatsInfo            NatsInfo                   `yaml:"nats_info"`
	Insights            *InsightsConfig            `yaml:"insights"`
	TurnServer          *TurnConfig                `yaml:"turn_server"`
	Hooks               *Hooks                     `yaml:"hooks"`
}

type ClientInfo struct {
	Port               int                 `yaml:"port"`
	Debug              bool                `yaml:"debug"`
	Path               string              `yaml:"path"`
	AssetHost          *string             `yaml:"asset_host"`
	ApiKey             string              `yaml:"api_key"`
	Secret             string              `yaml:"secret"`
	TokenValidity      *time.Duration      `yaml:"token_validity"`
	WebhookConf        WebhookConf         `yaml:"webhook_conf"`
	PrometheusConf     PrometheusConf      `yaml:"prometheus"`
	ProxyConf          *ProxyConf          `yaml:"proxy_conf"`
	CopyrightConf      *CopyrightConf      `yaml:"copyright_conf"`
	BBBJoinHost        *string             `yaml:"bbb_join_host"`
	AutoClientDownload *AutoClientDownload `yaml:"auto_client_download"`
}

type WebhookConf struct {
	Enable              bool   `yaml:"enable"`
	Url                 string `yaml:"url,omitempty"`
	EnableForPerMeeting bool   `yaml:"enable_for_per_meeting"`
}

type PrometheusConf struct {
	Enable      bool   `yaml:"enable"`
	MetricsPath string `yaml:"metrics_path"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
}

type ProxyConf struct {
	Enabled         bool     `yaml:"enabled"`
	ProxyHeader     string   `yaml:"proxy_header"`
	TrustedProxyIps []string `yaml:"trusted_proxy_ips"`
}

type LivekitInfo struct {
	Host   string `yaml:"host"`
	ApiKey string `yaml:"api_key"`
	Secret string `yaml:"secret"`
}

type LivekitSipInfo struct {
	Enabled            bool                       `yaml:"enabled"`
	TrunkName          string                     `yaml:"trunk_name"`
	PhoneNumbers       []string                   `yaml:"phone_numbers"`
	AllowedIpAddresses *[]string                  `yaml:"allowed_ip_addresses"`
	AuthUsername       *string                    `yaml:"auth_username"`
	AuthPassword       *string                    `yaml:"auth_password"`
	MediaEncryption    livekit.SIPMediaEncryption `yaml:"media_encryption"`
}

type UploadFileSettings struct {
	Path                  string   `yaml:"path"`
	MaxSize               uint64   `yaml:"max_size"`
	MaxSizeWhiteboardFile uint64   `yaml:"max_size_whiteboard_file"`
	KeepForever           bool     `yaml:"keep_forever"`
	AllowedTypes          []string `yaml:"allowed_types"`
}

type RecorderInfo struct {
	RecordingFilesPath string `yaml:"recording_files_path"`
	//How long generated token will valid to download the file
	TokenValidity time.Duration `yaml:"token_validity"`
	// How long to wait before considering a recorder inactive. default: 8 seconds
	PingTimeout                time.Duration `yaml:"ping_timeout"`
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
	RoomStreamName           string           `yaml:"room_stream_name"`
	Subjects                 NatsSubjects     `yaml:"subjects"`
	Recorder                 NatsInfoRecorder `yaml:"recorder"`
}

type NatsSubjects struct {
	SystemApiWorker  string `yaml:"system_api_worker"`
	SystemJsWorker   string `yaml:"system_js_worker"`   // jetstream worker
	SystemCoreWorker string `yaml:"system_core_worker"` // core pub/sub worker
	SystemPublic     string `yaml:"system_public"`
	SystemPrivate    string `yaml:"system_private"`
	Chat             string `yaml:"chat"`
	Whiteboard       string `yaml:"whiteboard"`
	DataChannel      string `yaml:"data_channel"`
}

type NatsInfoRecorder struct {
	RecorderChannel string `yaml:"recorder_channel"`
	RecorderInfoKv  string `yaml:"recorder_info_kv"`
	TranscodingJobs string `yaml:"transcoding_jobs_subject"`
}

// Hooks defines optional script pipelines for handling file I/O.
type Hooks struct {
	UploadHook          *hooks.HookScriptConfig `yaml:"upload_hook"`
	DownloadHook        *hooks.HookScriptConfig `yaml:"download_hook"`
	DeleteHook          *hooks.HookScriptConfig `yaml:"delete_hook"`
	ResumableUploadHook *hooks.HookScriptConfig `yaml:"resumable_upload_hook"`
	RoomEndHook         *hooks.HookScriptConfig `yaml:"room_end_hook"`
}

func InitAppConfig(ctx context.Context, appCnf *AppConfig) (*AppConfig, error) {
	// default validation of token is 10 minutes
	if appCnf.Client.TokenValidity == nil || *appCnf.Client.TokenValidity < 0 {
		appCnf.Client.TokenValidity = new(10 * time.Minute)
	}
	appCnf.ctx = ctx
	if appCnf.NatsInfo.RoomStreamName == "" {
		appCnf.NatsInfo.RoomStreamName = "pnm-room-stream"
	}
	if appCnf.NatsInfo.Subjects.SystemCoreWorker == "" {
		appCnf.NatsInfo.Subjects.SystemCoreWorker = "sysCoreWorker"
	}

	// set default values
	if appCnf.AnalyticsSettings != nil {
		//TODO: deprecated, will remove in future
		if appCnf.AnalyticsSettings.FilesStorePath == nil {
			appCnf.AnalyticsSettings.FilesStorePath = new("./analytics")
		}
	}

	if appCnf.RoomDefaultSettings == nil {
		appCnf.RoomDefaultSettings = &utils.RoomDefaultSettings{}
	}
	if appCnf.RoomDefaultSettings.MaxPreloadedWhiteboardFileSize == nil {
		appCnf.RoomDefaultSettings.MaxPreloadedWhiteboardFileSize = new("5mb")
	}

	bytes, err := utils.ParseFileSizeToBytes(*appCnf.RoomDefaultSettings.MaxPreloadedWhiteboardFileSize)
	if err != nil {
		logrus.WithError(err).Errorf("failed to parse max_preloaded_whiteboard_file_size, using default 5MB")
		bytes = 5 * 1024 * 1024
	}
	appCnf.RoomDefaultSettings.MaxPreloadedWhiteboardFileSizeByte = &bytes

	if !filepath.IsAbs(appCnf.LogSettings.LogFile) {
		appCnf.LogSettings.LogFile = filepath.Join(appCnf.RootWorkingDir, appCnf.LogSettings.LogFile)
	}
	appCnf.LogSettings.LogFile = filepath.Clean(appCnf.LogSettings.LogFile)

	if !filepath.IsAbs(appCnf.UploadFileSettings.Path) {
		appCnf.UploadFileSettings.Path = filepath.Join(appCnf.RootWorkingDir, appCnf.UploadFileSettings.Path)
	}
	appCnf.UploadFileSettings.Path = filepath.Clean(appCnf.UploadFileSettings.Path)

	if !filepath.IsAbs(appCnf.RecorderInfo.RecordingFilesPath) {
		appCnf.RecorderInfo.RecordingFilesPath = filepath.Join(appCnf.RootWorkingDir, appCnf.RecorderInfo.RecordingFilesPath)
		appCnf.RecorderInfo.DelRecordingBackupPath = filepath.Join(appCnf.RootWorkingDir, appCnf.RecorderInfo.DelRecordingBackupPath)
	}
	appCnf.RecorderInfo.RecordingFilesPath = filepath.Clean(appCnf.RecorderInfo.RecordingFilesPath)
	appCnf.RecorderInfo.DelRecordingBackupPath = filepath.Clean(appCnf.RecorderInfo.DelRecordingBackupPath)

	if appCnf.RecorderInfo.PingTimeout == 0 {
		appCnf.RecorderInfo.PingTimeout = time.Second * 8
	}

	// setup everything for artifacts
	err = handleArtifactsSettings(appCnf)
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
		appCnf.RecorderInfo.DelRecordingBackupPath = filepath.Clean(appCnf.RecorderInfo.DelRecordingBackupPath)

		if err := os.MkdirAll(appCnf.RecorderInfo.DelRecordingBackupPath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create recording backup directory %s: %w", appCnf.RecorderInfo.DelRecordingBackupPath, err)
		}
	}

	if appCnf.DatabaseInfo.Prefix != "" {
		dbTablePrefix = appCnf.DatabaseInfo.Prefix
	}
	if appCnf.NatsInfo.Recorder.TranscodingJobs == "" {
		appCnf.NatsInfo.Recorder.TranscodingJobs = "pnm-RecorderTranscoderJobs"
	}
	if appCnf.LivekitSipInfo != nil && appCnf.LivekitSipInfo.Enabled {
		if len(appCnf.LivekitSipInfo.PhoneNumbers) == 0 {
			return nil, fmt.Errorf("at least one SIP inbound phone number required in `phone_numbers`")
		}
		if appCnf.LivekitSipInfo.TrunkName == "" {
			appCnf.LivekitSipInfo.TrunkName = "pnm-inbound-trunk"
		}
	}

	// handle client download
	if appCnf.Client.AutoClientDownload != nil {
		if err := appCnf.Client.AutoClientDownload.Handle(appCnf); err != nil {
			return nil, err
		}
	}

	// setting up upload files setting
	if appCnf.UploadFileSettings.MaxSize <= 0 {
		appCnf.UploadFileSettings.MaxSize = 50
	}
	if appCnf.UploadFileSettings.MaxSizeWhiteboardFile <= 0 {
		appCnf.UploadFileSettings.MaxSizeWhiteboardFile = 30
	}
	if len(appCnf.UploadFileSettings.AllowedTypes) == 0 {
		appCnf.UploadFileSettings.AllowedTypes = []string{"jpg", "png", "jpeg", "svg", "pdf", "docx", "txt", "xlsx", "pptx", "zip", "mp4", "webm", "mp3"}
	}
	if appCnf.UploadFileSettings.MaxSizeWhiteboardFile > appCnf.UploadFileSettings.MaxSize {
		return nil, fmt.Errorf("max_size_whiteboard_file value should not be more than max_size")
	}

	// read client files and cache it
	if err := readClientFiles(appCnf); err != nil {
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
		appCnf.ArtifactsSettings.StoragePath = new("./artifacts")
	}
	if appCnf.ArtifactsSettings.TokenValidity == nil {
		appCnf.ArtifactsSettings.TokenValidity = new(10 * time.Minute)
	}

	p := *appCnf.ArtifactsSettings.StoragePath
	if !filepath.IsAbs(p) {
		p = filepath.Join(appCnf.RootWorkingDir, p)
		appCnf.ArtifactsSettings.StoragePath = &p
	}
	p = filepath.Clean(p)
	appCnf.ArtifactsSettings.StoragePath = &p

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
		if !filepath.IsAbs(trashPath) {
			trashPath = filepath.Join(appCnf.RootWorkingDir, trashPath)
		}
		appCnf.ArtifactsSettings.DelArtifactsBackupPath = filepath.Clean(trashPath)

		err := os.MkdirAll(trashPath, 0755)
		if err != nil {
			return fmt.Errorf("failed to create artifacts backup directory %s: %w", trashPath, err)
		}
	}
	return nil
}

func InitializeHooks(ctx context.Context, appCnf *AppConfig) error {
	if appCnf.Hooks == nil {
		return nil // Feature is not enabled.
	}

	resolvePath := func(scriptPath string) string {
		if !filepath.IsAbs(scriptPath) {
			p := filepath.Join(appCnf.RootWorkingDir, scriptPath)
			return filepath.Clean(p)
		}
		return scriptPath
	}

	// scriptsWithPoolSize maps each unique script path to its required pool size.
	// We take the max pool size if a script is used in multiple categories.
	scriptsWithPoolSize := make(map[string]int)

	processHookCategory := func(config *hooks.HookScriptConfig, name string) error {
		if config == nil {
			return nil
		}
		if config.PoolSize <= 0 {
			config.PoolSize = 1
		}
		if config.HookTimeout == 0 {
			config.HookTimeout = 5 * time.Minute
		}
		for i, script := range config.Scripts {
			var resolved string
			if strings.HasPrefix(script.Script, hooks.HookCommandHttpRequest) {
				resolved = script.Script
			} else {
				resolved = resolvePath(script.Script)
			}

			if err := hooks.ValidateHookScript(resolved, name); err != nil {
				return err
			}
			config.Scripts[i].Script = resolved

			if !script.IsOneShot {
				// If the same script is used in multiple hooks, use the larger pool size.
				if currentSize, ok := scriptsWithPoolSize[resolved]; !ok || config.PoolSize > currentSize {
					scriptsWithPoolSize[resolved] = config.PoolSize
				}
			}
		}
		return nil
	}

	if err := processHookCategory(appCnf.Hooks.UploadHook, "upload_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.DownloadHook, "download_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.DeleteHook, "delete_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.ResumableUploadHook, "resumable_upload_hook"); err != nil {
		return err
	}
	if err := processHookCategory(appCnf.Hooks.RoomEndHook, "room_end_hook"); err != nil {
		return err
	}

	// Initialize the HookProcessManager and start all unique scripts
	appCnf.HookManager = hooks.NewHookProcessManager(ctx, appCnf.Logger.WithField("service", "hook_manager"))
	if err := appCnf.HookManager.StartHookProcesses(scriptsWithPoolSize); err != nil {
		return fmt.Errorf("failed to start hook processes: %w", err)
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

	if !filepath.IsAbs(a.Client.Path) {
		a.Client.Path = filepath.Join(a.RootWorkingDir, a.Client.Path)
	}
	a.Client.Path = filepath.Clean(a.Client.Path)

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

func IsUserIdInternal(userId string) bool {
	return strings.HasPrefix(userId, IngressUserIdPrefix) || strings.HasPrefix(userId, AgentUserUserIdPrefix) || strings.HasPrefix(userId, SipUserIdPrefix) || strings.HasPrefix(userId, TTSAgentUserIdPrefix)
}
