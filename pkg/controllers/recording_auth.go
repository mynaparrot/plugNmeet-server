package controllers

import (
	"github.com/bufbuild/protovalidate-go"
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
	v, err := protovalidate.New()
	if err != nil {
		utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
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

func HandleRecordingInfo(c *fiber.Ctx) error {
	req := new(plugnmeet.RecordingInfoReq)
	op := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	v, err := protovalidate.New()
	if err != nil {
		utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	m := models.NewRecordingAuth()
	result, err := m.RecordingInfo(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	return utils.SendProtoJsonResponse(c, result)
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
	v, err := protovalidate.New()
	if err != nil {
		utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
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
	v, err := protovalidate.New()
	if err != nil {
		utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
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
		return c.Status(fiber.StatusUnauthorized).SendString("token require or invalid url")
	}

	m := models.NewRecordingAuth()
	file, status, err := m.VerifyRecordingToken(token)

	if err != nil {
		return c.Status(status).SendString(err.Error())
	}

	c.Attachment(file)
	return c.SendFile(file, false)
}
