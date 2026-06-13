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
)

// ArtifactController holds the dependencies for artifact-related handlers.
type ArtifactController struct {
	ArtifactModel *models.ArtifactModel
}

// NewArtifactController creates a new ArtifactController.
func NewArtifactController(am *models.ArtifactModel) *ArtifactController {
	return &ArtifactController{
		ArtifactModel: am,
	}
}

// HandleFetchArtifacts fetches a paginated list of artifacts.
func (ac *ArtifactController) HandleFetchArtifacts(c fiber.Ctx) error {
	req := new(plugnmeet.FetchArtifactsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := ac.ArtifactModel.FetchArtifacts(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "no artifact found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.FetchArtifactsRes{
		Status:     true,
		Msg:        "success",
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Result:     result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleGetArtifactDownloadToken generates a download token for a downloadable artifact.
func (ac *ArtifactController) HandleGetArtifactDownloadToken(c fiber.Ctx) error {
	req := new(plugnmeet.GetArtifactDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	token, err := ac.ArtifactModel.GetArtifactDownloadToken(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "artifact not found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.GetArtifactDownloadTokenRes{
		Status:     true,
		Msg:        "success",
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Token:      &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadArtifact handles the download of an artifact file using a JWT.
func (ac *ArtifactController) HandleDownloadArtifact(c fiber.Ctx) error {
	token := c.Params("token")
	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token required or invalid url")
	}

	res, status, err := ac.ArtifactModel.VerifyArtifactDownloadJWT(token)
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

func (ac *ArtifactController) HandleGetArtifactInfo(c fiber.Ctx) error {
	req := new(plugnmeet.ArtifactInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	res, err := ac.ArtifactModel.GetArtifactInfoByArtifactId(req.ArtifactId)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "artifact not found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	return utils.SendProtoJsonResponse(c, res)
}

// HandleDeleteArtifact deletes an artifact.
func (ac *ArtifactController) HandleDeleteArtifact(c fiber.Ctx) error {
	req := new(plugnmeet.DeleteArtifactReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if err := ac.ArtifactModel.DeleteArtifact(req); err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, "artifact not found", plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, err.Error(), plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success", plugnmeet.StatusCode_SUCCESS)
}
