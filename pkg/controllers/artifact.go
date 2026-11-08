package controllers

import (
	"errors"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
)

// ArtifactController holds the dependencies for artifact-related handlers.
type ArtifactController struct {
	ArtifactModel *models.ArtifactModel
	logger        *logrus.Entry
}

type ArtifactControllerArgs struct {
	fx.In
	ArtifactModel *models.ArtifactModel
	Logger        *logrus.Logger
}

// NewArtifactController creates a new ArtifactController.
func NewArtifactController(args ArtifactControllerArgs) *ArtifactController {
	return &ArtifactController{
		ArtifactModel: args.ArtifactModel,
		logger:        args.Logger.WithField("controller", "artifact"),
	}
}

// HandleFetchArtifacts fetches a paginated list of artifacts.
func (ac *ArtifactController) HandleFetchArtifacts(c fiber.Ctx) error {
	req := new(plugnmeet.FetchArtifactsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	result, err := ac.ArtifactModel.FetchArtifacts(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactsFetchFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.FetchArtifactsRes{
		Status:     true,
		Msg:        config.MsgIntegratorSuccess,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Result:     result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleGetArtifactDownloadToken generates a download token for a downloadable artifact.
func (ac *ArtifactController) HandleGetArtifactDownloadToken(c fiber.Ctx) error {
	req := new(plugnmeet.GetArtifactDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	token, err := ac.ArtifactModel.GetArtifactDownloadToken(req)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthDownloadTokenFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	r := &plugnmeet.GetArtifactDownloadTokenRes{
		Status:     true,
		Msg:        config.MsgIntegratorSuccess,
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Token:      &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadArtifact handles the download of an artifact file using a JWT.
func (ac *ArtifactController) HandleDownloadArtifact(c fiber.Ctx) error {
	token := c.Params("token")
	if token == "" {
		return c.Status(fiber.StatusUnauthorized).SendString(config.MsgFileDownloadTokenInvalid)
	}

	res, status, err := ac.ArtifactModel.VerifyArtifactDownloadJWT(token)
	if err != nil {
		return downloadTokenErrorResponse(c, res, status, err, ac.logger)
	}

	return serveDownloadHookResult(c, res, ac.logger)
}

func (ac *ArtifactController) HandleGetArtifactInfo(c fiber.Ctx) error {
	req := new(plugnmeet.ArtifactInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	res, err := ac.ArtifactModel.GetArtifactInfoByArtifactId(req.ArtifactId)
	if err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactsFetchFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}
	return utils.SendProtoJsonResponse(c, res)
}

// HandleDeleteArtifact deletes an artifact.
func (ac *ArtifactController) HandleDeleteArtifact(c fiber.Ctx) error {
	req := new(plugnmeet.DeleteArtifactReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthInvalidRequest, plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if err := ac.ArtifactModel.DeleteArtifact(req); err != nil {
		if errors.Is(err, config.NotFoundErr) {
			return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactNotFound, plugnmeet.StatusCode_NOT_FOUND)
		}
		return utils.SendCommonProtoJsonResponse(c, false, config.MsgAuthArtifactDeleteFailed, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR)
	}

	return utils.SendCommonProtoJsonResponse(c, true, config.MsgIntegratorSuccess, plugnmeet.StatusCode_SUCCESS)
}
