package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
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
func (ac *ArtifactController) HandleFetchArtifacts(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchArtifactsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	result, err := ac.ArtifactModel.FetchArtifacts(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if result.GetTotalArtifacts() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no artifacts found")
	}

	r := &plugnmeet.FetchArtifactsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleGetArtifactDownloadToken generates a download token for a downloadable artifact.
func (ac *ArtifactController) HandleGetArtifactDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetArtifactDownloadTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	token, err := ac.ArtifactModel.GetArtifactDownloadToken(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GetArtifactDownloadTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleDownloadArtifact handles the download of an artifact file using a JWT.
func (ac *ArtifactController) HandleDownloadArtifact(c *fiber.Ctx) error {
	token := c.Params("token")
	if len(token) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("token required or invalid url")
	}

	filePath, fileName, err := ac.ArtifactModel.VerifyArtifactDownloadJWT(token)
	if err != nil {
		// Use fiber.StatusBadRequest for client-side errors like invalid tokens.
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}

	c.Attachment(fileName)
	return c.SendFile(filePath, false)
}

func (ac *ArtifactController) HandleGetArtifactInfo(c *fiber.Ctx) error {
	req := new(plugnmeet.ArtifactDetailsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	res, err := ac.ArtifactModel.GetArtifactDetails(req.ArtifactId)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	return utils.SendProtoJsonResponse(c, res)
}

// HandleDeleteArtifact deletes an artifact.
func (ac *ArtifactController) HandleDeleteArtifact(c *fiber.Ctx) error {
	req := new(plugnmeet.DeleteArtifactReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	err := ac.ArtifactModel.DeleteArtifact(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success")
}
