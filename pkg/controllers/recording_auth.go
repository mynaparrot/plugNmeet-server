package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/encoding/protojson"
)

func HandleFetchRecordings(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchRecordingsReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	err = req.Validate()
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	m := models.NewRecordingAuth()
	result, err := m.FetchRecordings(req)

	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	if result.GetTotalRecordings() == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "no recordings found")
	}

	r := &plugnmeet.FetchRecordingsRes{
		Status: true,
		Msg:    "success",
		Result: result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

func HandleDeleteRecording(c *fiber.Ctx) error {
	req := new(plugnmeet.DeleteRecordingReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	err = req.Validate()
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	m := models.NewRecordingAuth()
	err = m.DeleteRecording(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendCommonProtoJsonResponse(c, true, "success")
}

func HandleGetDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetDownloadTokenReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}
	err = req.Validate()
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	m := models.NewRecordingAuth()
	token, err := m.GetDownloadToken(req)

	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GetDownloadTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}
	return utils.SendProtoJsonResponse(c, r)
}

func HandleDownloadRecording(c *fiber.Ctx) error {
	token := c.Params("token")

	if len(token) == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "token require or invalid url")
	}

	m := models.NewRecordingAuth()
	file, err := m.VerifyRecordingToken(token)

	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	c.Attachment(file)
	return c.SendFile(file, false)
}
