package config

import (
	"database/sql"
	"github.com/go-playground/validator/v10"
	"github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

var AppCnf *AppConfig

type AppConfig struct {
	DB        *sql.DB
	RDS       *redis.Client
	chatRooms map[string]map[string]*ChatParticipant
	mux       *sync.RWMutex

	Client             ClientInfo         `yaml:"client"`
	LogSettings        LogSettings        `yaml:"log_settings"`
	LivekitInfo        LivekitInfo        `yaml:"livekit_info"`
	RedisInfo          RedisInfo          `yaml:"redis_info"`
	MySqlInfo          MySqlInfo          `yaml:"mysql_info"`
	UploadFileSettings UploadFileSettings `yaml:"upload_file_settings"`
	RecorderInfo       RecorderInfo       `yaml:"recorder_info"`
	SharedNotePad      SharedNotePad      `yaml:"shared_notepad"`
}

type ClientInfo struct {
	Port           int            `yaml:"port"`
	Debug          bool           `yaml:"debug"`
	Path           string         `yaml:"path"`
	ApiKey         string         `yaml:"api_key"`
	Secret         string         `yaml:"secret"`
	WebhookConf    WebhookConf    `yaml:"webhook_conf"`
	PrometheusConf PrometheusConf `yaml:"prometheus"`
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

type RedisInfo struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DBName   int    `yaml:"db"`
}

type MySqlInfo struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DBName   string `yaml:"db"`
	Prefix   string `yaml:"prefix"`
}

type UploadFileSettings struct {
	Path         string   `yaml:"path"`
	MaxSize      int      `yaml:"max_size"`
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

type ChatParticipant struct {
	RoomSid string
	RoomId  string
	Name    string
	UserSid string
	UserId  string
	UUID    string
}

func SetAppConfig(a *AppConfig) {
	AppCnf = a
	AppCnf.chatRooms = make(map[string]map[string]*ChatParticipant)
	AppCnf.mux = &sync.RWMutex{}
	setLogger()
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

type ErrorResponse struct {
	FailedField string
	Tag         string
}

func (a *AppConfig) DoValidateReq(r interface{}) []*ErrorResponse {
	var errors []*ErrorResponse

	validate := validator.New()
	_ = validate.RegisterValidation("require-valid-Id", ValidateId)
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})

	err := validate.Struct(r)
	if err != nil {
		for _, err := range err.(validator.ValidationErrors) {
			var element ErrorResponse
			element.FailedField = err.Field()
			element.Tag = err.Tag()
			errors = append(errors, &element)
		}
	}
	return errors
}

func ValidateId(fl validator.FieldLevel) bool {
	isValid := regexp.MustCompile(`^[a-zA-Z0-9\-\_]+$`).MatchString
	return isValid(fl.Field().String())
}

func (a *AppConfig) FormatDBTable(table string) string {
	if a.MySqlInfo.Prefix != "" {
		return a.MySqlInfo.Prefix + table
	}
	return table
}

func (a *AppConfig) AddChatUser(roomId string, participant *ChatParticipant) {
	a.mux.Lock()
	_, ok := a.chatRooms[roomId]
	a.mux.Unlock()

	if !ok {
		a.mux.Lock()
		a.chatRooms[roomId] = make(map[string]*ChatParticipant)
		a.mux.Unlock()
	}
	a.mux.Lock()
	a.chatRooms[roomId][participant.UserId] = participant
	a.mux.Unlock()
}

func (a *AppConfig) GetChatParticipants(roomId string) map[string]*ChatParticipant {
	a.mux.Lock()
	participants := a.chatRooms[roomId]
	a.mux.Unlock()

	return participants
}

func (a *AppConfig) RemoveChatParticipant(roomId, userId string) {
	a.mux.Lock()
	_, ok := a.chatRooms[roomId]
	a.mux.Unlock()

	if ok {
		a.mux.Lock()
		delete(a.chatRooms[roomId], userId)
		a.mux.Unlock()
	}
}
func (a *AppConfig) DeleteChatRoom(roomId string) {
	a.mux.Lock()
	_, ok := a.chatRooms[roomId]
	a.mux.Unlock()

	if ok {
		a.mux.Lock()
		delete(a.chatRooms, roomId)
		a.mux.Unlock()
	}
}
