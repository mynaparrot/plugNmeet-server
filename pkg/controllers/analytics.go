package controllers

import (
	"errors"
	"path/filepath"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
)

// AnalyticsController holds the dependencies for analytics-related handlers.
type AnalyticsController struct {
	AnalyticsModel *models.AnalyticsModel
	artifactModel  *models.ArtifactModel
}

type AnalyticsControllerArgs struct {
	fx.In
	AnalyticsModel *models.AnalyticsModel
	ArtifactModel  *models.ArtifactModel
}

// NewAnalyticsController creates a new AnalyticsController.
func NewAnalyticsController(args AnalyticsControllerArgs) *AnalyticsController {
	return &AnalyticsController{
		AnalyticsModel: args.AnalyticsModel,
		artifactModel:  args.ArtifactModel,
	}
}

// HandleFetchAnalytics fetches analytics data.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleFetchAnalytics(c fiber.Ctx) error {
	req := new(plugnmeet.FetchAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return sendCommonProtoJsonResponse(c, false, config.ErrArtifactInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := ac.AnalyticsModel.FetchAnalytics(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactNotFound.Error(), plugnmeet.StatusCode_NOT_FOUND)
		}
		return sendCommonProtoJsonResponse(c, false, config.ErrAnalyticsFetchFailed.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	msg, msgKey := callerMsgWithKey(c, config.MsgSuccess)
	r := &plugnmeet.FetchAnalyticsRes{
		Status:     true,
		Msg:        msg,
		MsgKey:     msgKey,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Result:     result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDeleteAnalytics deletes analytics data.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleDeleteAnalytics(c fiber.Ctx) error {
	req := new(plugnmeet.DeleteAnalyticsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return sendCommonProtoJsonResponse(c, false, config.ErrArtifactInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	newReq := &plugnmeet.DeleteArtifactReq{
		ArtifactId: req.FileId,
	}
	err := ac.artifactModel.DeleteArtifact(newReq)
	if err != nil {
		switch {
		case errors.Is(err, config.NotFoundErr):
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactNotFound.Error(), plugnmeet.StatusCode_NOT_FOUND)
		case errors.Is(err, config.ErrArtifactDeleteNotAllowed):
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactDeleteNotAllowed.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
		}
		return sendCommonProtoJsonResponse(c, false, config.ErrArtifactDeleteFailed.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return sendCommonProtoJsonResponse(c, true, config.MsgSuccess, plugnmeet.StatusCode_SUCCESS)
}

// HandleGetAnalyticsDownloadToken generates a download token for analytics.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleGetAnalyticsDownloadToken(c fiber.Ctx) error {
	req := new(plugnmeet.GetAnalyticsDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return sendCommonProtoJsonResponse(c, false, config.ErrArtifactInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	newReq := &plugnmeet.GetArtifactDownloadTokenReq{
		ArtifactId: req.FileId,
	}
	token, err := ac.artifactModel.GetArtifactDownloadToken(newReq)
	if err != nil {
		switch {
		case errors.Is(err, config.NotFoundErr):
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactNotFound.Error(), plugnmeet.StatusCode_NOT_FOUND)
		case errors.Is(err, config.ErrArtifactNotDownloadable):
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactNotDownloadable.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
		case errors.Is(err, config.ErrArtifactNoDownloadableFile):
			return sendCommonProtoJsonResponse(c, false, config.ErrArtifactNoDownloadableFile.Error(), plugnmeet.StatusCode_NOT_FOUND)
		}
		return sendCommonProtoJsonResponse(c, false, config.ErrArtifactDownloadTokenFailed.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	msg, msgKey := callerMsgWithKey(c, config.MsgSuccess)
	r := &plugnmeet.GetAnalyticsDownloadTokenRes{
		Status:     true,
		Msg:        msg,
		MsgKey:     msgKey,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Token:      &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadAnalytics handles the download of an analytics file.
// Deprecated: only for backward compatibility
func (ac *AnalyticsController) HandleDownloadAnalytics(c fiber.Ctx) error {
	token := c.Params("token")
	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token required or invalid url")
	}

	res, status, err := ac.artifactModel.VerifyArtifactDownloadJWT(token)
	if err != nil {
		return c.Status(status).SendString(err.Error())
	}

	switch res.Action {
	case hooks.DownloadHookDataActionRedirect:
		if res.RedirectUrl == "" {
			return c.Status(fiber.StatusInternalServerError).SendString("hook script did not provide a redirect_url")
		}
		return c.Redirect().Status(fiber.StatusTemporaryRedirect).To(res.RedirectUrl)
	case hooks.DownloadHookDataActionServeLocal:
		if res.OutputPath == "" {
			return c.Status(fiber.StatusInternalServerError).SendString("hook script did not provide a local_path")
		}
		if res.MimeType == "" {
			return c.Status(fiber.StatusInternalServerError).SendString("hook script did not provide a mime_type")
		}
		c.Set(fiber.HeaderContentType, res.MimeType)
		c.Set(fiber.HeaderContentDisposition, "attachment; filename="+filepath.Base(res.OutputPath))
		return c.SendFile(res.OutputPath)
	default:
		return c.Status(fiber.StatusInternalServerError).SendString("invalid action from download hook")
	}
}
