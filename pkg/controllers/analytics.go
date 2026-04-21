package controllers

import (
	"errors"

	"buf.build/go/protovalidate"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
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
	artifactModel  *models.ArtifactModel
}

// NewAnalyticsController creates a new AnalyticsController.
func NewAnalyticsController(am *models.AnalyticsModel, artifactModel *models.ArtifactModel) *AnalyticsController {
	return &AnalyticsController{
		AnalyticsModel: am,
		artifactModel:  artifactModel,
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
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleFetchAnalytics(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := ac.AnalyticsModel.FetchAnalytics(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	if result.GetTotalAnalytics() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no analytics found", plugnmeet.StatusCode_NOT_FOUND)
	}

	r := &plugnmeet.FetchAnalyticsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDeleteAnalytics deletes analytics data.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleDeleteAnalytics(c *fiber.Ctx) error {
	req := new(plugnmeet.DeleteAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	newReq := &plugnmeet.DeleteArtifactReq{
		ArtifactId: req.FileId,
	}
	err := ac.artifactModel.DeleteArtifact(newReq)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "artifact not found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success", plugnmeet.StatusCode_SUCCESS)
}

// HandleGetAnalyticsDownloadToken generates a download token for analytics.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleGetAnalyticsDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetAnalyticsDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	newReq := &plugnmeet.GetArtifactDownloadTokenReq{
		ArtifactId: req.FileId,
	}
	token, err := ac.artifactModel.GetArtifactDownloadToken(newReq)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "artifact not found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.GetAnalyticsDownloadTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadAnalytics handles the download of an analytics file.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleDownloadAnalytics(c *fiber.Ctx) error {
	token := c.Params("token")
	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token required or invalid url")
	}

	filePath, fileName, err := ac.artifactModel.VerifyArtifactDownloadJWT(token)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}

	c.Attachment(fileName)
	return c.SendFile(filePath, false)
}
