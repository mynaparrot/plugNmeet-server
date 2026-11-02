package models

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

type LtiV1Model struct {
	app    *config.AppConfig
	rm     *RoomModel
	um     *UserModel
	logger *logrus.Entry
}

type LTIV1FetchRecordingsReq struct {
	From    uint32 `json:"from"`
	Limit   uint32 `json:"limit"`
	OrderBy string `json:"order_by"`
}

type LtiV1ModelArgs struct {
	fx.In
	App *config.AppConfig
	Rm  *RoomModel
	Um  *UserModel
}

func NewLtiV1Model(args LtiV1ModelArgs) *LtiV1Model {
	return &LtiV1Model{
		app:    args.App,
		rm:     args.Rm,
		um:     args.Um,
		logger: args.Rm.logger.Logger.WithField("model", "lti_v1"),
	}
}

func (m *LtiV1Model) LTIV1Landing(c fiber.Ctx, requests, signingURL string) error {
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
		return config.UserIdOrEmailRequired
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

	if claims.LtiCustomParameters != nil && claims.LtiCustomParameters.LtiCustomDesign != nil {
		design, err := json.Marshal(claims.LtiCustomParameters.LtiCustomDesign)
		if err == nil {
			vals["CustomDesign"] = string(design)
		}
	}

	return c.Render("assets/lti/v1", vals)
}
