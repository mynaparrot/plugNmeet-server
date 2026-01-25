package models

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

type LtiV1Model struct {
	app    *config.AppConfig
	rm     *RoomModel
	um     *UserModel
	logger *logrus.Entry
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

func NewLtiV1Model(app *config.AppConfig, rm *RoomModel, um *UserModel) *LtiV1Model {
	return &LtiV1Model{
		app:    app,
		rm:     rm,
		um:     um,
		logger: rm.logger.Logger.WithField("model", "lti_v1"),
	}
}

func (m *LtiV1Model) LTIV1Landing(c *fiber.Ctx, requests, signingURL string) error {
	params, err := m.VerifyAuth(requests, signingURL)
	if err != nil {
		return err
	}

	roomId := fmt.Sprintf("%s_%s_%s", params.Get("tool_consumer_instance_guid"), params.Get("context_id"), params.Get("resource_link_id"))

	userId := params.Get("user_id")
	if userId == "" {
		userId = m.genHashId(params.Get("lis_person_contact_email_primary"))
	}

	if userId == "" {
		return fmt.Errorf(config.UserIdOrEmailRequired)
	}

	name := params.Get("lis_person_name_full")
	if name == "" {
		name = fmt.Sprintf("%s_%s", "User", userId)
	}

	claims := &plugnmeet.LtiClaims{
		UserId:    userId,
		Name:      name,
		IsAdmin:   false,
		RoomId:    m.genHashId(roomId),
		RoomTitle: params.Get("context_label"),
	}

	if strings.Contains(params.Get("roles"), "Instructor") {
		claims.IsAdmin = true
	}
	utils.AssignLTIV1CustomParams(params, claims)

	j, err := m.ToJWT(claims)
	if err != nil {
		return err
	}

	vals := fiber.Map{
		"Title":   claims.RoomTitle,
		"Token":   j,
		"IsAdmin": claims.IsAdmin,
	}

	if claims.LtiCustomParameters.LtiCustomDesign != nil {
		design, err := json.Marshal(claims.LtiCustomParameters.LtiCustomDesign)
		if err == nil {
			vals["CustomDesign"] = string(design)
		}
	}

	return c.Render("assets/lti/v1", vals)
}
