package ltiv1model

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models/room"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/livekit"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
)

type LtiV1Model struct {
	app *config.AppConfig
	ds  *dbservice.DatabaseService
	rs  *redisservice.RedisService
	lk  *livekitservice.LivekitService
	rm  *roommodel.RoomModel
}

type LtiClaims struct {
	UserId              string               `json:"user_id"`
	Name                string               `json:"name"`
	IsAdmin             bool                 `json:"is_admin"`
	RoomId              string               `json:"room_id"`
	RoomTitle           string               `json:"room_title"`
	LtiCustomParameters *LtiCustomParameters `json:"lti_custom_parameters,omitempty"`
}

type LtiCustomParameters struct {
	RoomDuration               uint64           `json:"room_duration,omitempty"`
	AllowPolls                 *bool            `json:"allow_polls,omitempty"`
	AllowSharedNotePad         *bool            `json:"allow_shared_note_pad,omitempty"`
	AllowBreakoutRoom          *bool            `json:"allow_breakout_room,omitempty"`
	AllowRecording             *bool            `json:"allow_recording,omitempty"`
	AllowRTMP                  *bool            `json:"allow_rtmp,omitempty"`
	AllowViewOtherWebcams      *bool            `json:"allow_view_other_webcams,omitempty"`
	AllowViewOtherParticipants *bool            `json:"allow_view_other_users_list,omitempty"`
	MuteOnStart                *bool            `json:"mute_on_start,omitempty"`
	LtiCustomDesign            *LtiCustomDesign `json:"lti_custom_design,omitempty"`
}

type LtiCustomDesign struct {
	PrimaryColor    string `json:"primary_color,omitempty"`
	SecondaryColor  string `json:"secondary_color,omitempty"`
	BackgroundColor string `json:"background_color,omitempty"`
	CustomLogo      string `json:"custom_logo,omitempty"`
}

type LTIV1FetchRecordingsReq struct {
	From    uint32 `json:"from"`
	Limit   uint32 `json:"limit"`
	OrderBy string `json:"order_by"`
}

func New(app *config.AppConfig, ds *dbservice.DatabaseService, rs *redisservice.RedisService, lk *livekitservice.LivekitService) *LtiV1Model {
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

	return &LtiV1Model{
		app: app,
		ds:  ds,
		rs:  rs,
		lk:  lk,
		rm:  roommodel.New(app, ds, rs, lk),
	}
}
