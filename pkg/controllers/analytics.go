package controllers

import (
	"buf.build/go/protovalidate"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var unmarshalOpts = protojson.UnmarshalOptions{
	DiscardUnknown: true,
}

// AnalyticsController holds the dependencies for analytics-related handlers.
type AnalyticsController struct {
	AnalyticsModel *models.AnalyticsModel
}

// NewAnalyticsController creates a new AnalyticsController.
func NewAnalyticsController(am *models.AnalyticsModel) *AnalyticsController {
	return &AnalyticsController{
		AnalyticsModel: am,
	}
}

func parseAndValidateRequest(data []byte, msg proto.Message) error {
	err := unmarshalOpts.Unmarshal(data, msg)
	if err != nil {
		return err
	}
	return validateRequest(msg)
}

func validateRequest(msg proto.Message) error {
	v, err := protovalidate.New()
	if err != nil {
		return err
	}

	if err = v.Validate(msg); err != nil {
		return err
	}
	return nil
}

// HandleFetchAnalytics fetches analytics data.
func (ac *AnalyticsController) HandleFetchAnalytics(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	result, err := ac.AnalyticsModel.FetchAnalytics(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if result.GetTotalAnalytics() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no analytics found")
	}

	r := &plugnmeet.FetchAnalyticsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDeleteAnalytics deletes analytics data.
func (ac *AnalyticsController) HandleDeleteAnalytics(c *fiber.Ctx) error {
	req := new(plugnmeet.DeleteAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	err := ac.AnalyticsModel.DeleteAnalytics(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success")
}

// HandleGetAnalyticsDownloadToken generates a download token for analytics.
func (ac *AnalyticsController) HandleGetAnalyticsDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetAnalyticsDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	token, err := ac.AnalyticsModel.GetAnalyticsDownloadToken(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GetAnalyticsDownloadTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadAnalytics handles the download of an analytics file.
func (ac *AnalyticsController) HandleDownloadAnalytics(c *fiber.Ctx) error {
	token := c.Params("token")
	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token require or invalid url")
	}

	file, status, err := ac.AnalyticsModel.VerifyAnalyticsToken(token)
	if err != nil {
		return c.Status(status).SendString(err.Error())
	}

	c.Attachment(file)
	return c.SendFile(file, false)
}
