package controllers

import (
	"github.com/bufbuild/protovalidate-go"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"google.golang.org/protobuf/proto"
)

func HandleGenerateJoinToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GenerateTokenReq)
	err := op.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	v, err := protovalidate.New()
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, "failed to initialize validator: "+err.Error())
	}

	if err = v.Validate(req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if req.UserInfo == nil {
		return utils.SendCommonProtoJsonResponse(c, false, "UserInfo required")
	}

	// don't generate token if user is blocked
	nts := natsservice.New(config.GetConfig())
	exist := nts.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return utils.SendCommonProtoJsonResponse(c, false, "this user is blocked to join this session")
	}

	ds := dbservice.New(config.GetConfig().DB)
	ri, _ := ds.GetRoomInfoByRoomId(req.RoomId, 1)
	if ri == nil || ri.ID == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "room is not active. create room first")
	}

	m := models.NewUserModel(nil, nil, nil)
	token, err := m.GetPNMJoinToken(req)
	if err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	r := &plugnmeet.GenerateTokenRes{
		Status: true,
		Msg:    "success",
		Token:  &token,
	}

	return utils.SendProtoJsonResponse(c, r)
}

func HandleUpdateUserLockSetting(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.UpdateUserLockSettingsReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	// now need to check if meeting is running or not
	app := config.GetConfig()
	ds := dbservice.New(app.DB)
	isRunning := 1
	room, _ := ds.GetRoomInfoBySid(req.RoomSid, &isRunning)

	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	m := models.NewUserModel(nil, nil, nil)
	err = m.UpdateUserLockSettings(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleMuteUnMuteTrack(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	app := config.GetConfig()
	ds := dbservice.New(app.DB)
	m := models.NewUserModel(app, ds, nil)

	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req := new(plugnmeet.MuteUnMuteTrackReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}

	// now need to check if meeting is running or not
	isRunning := 1
	room, _ := ds.GetRoomInfoBySid(req.Sid, &isRunning)
	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	err = m.MuteUnMuteTrack(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleRemoveParticipant(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	app := config.GetConfig()
	ds := dbservice.New(app.DB)
	m := models.NewUserModel(app, ds, nil)
	err := m.CommonValidation(c)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req := new(plugnmeet.RemoveParticipantReq)
	err = proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	if roomId != req.RoomId {
		return utils.SendCommonProtobufResponse(c, false, "requested roomId & token roomId mismatched")
	}
	if requestedUserId == req.UserId {
		return utils.SendCommonProtobufResponse(c, false, "you can't remove yourself\"")
	}

	// now need to check if meeting is running or not
	isRunning := 1
	room, _ := ds.GetRoomInfoBySid(req.Sid, &isRunning)
	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	err = m.RemoveParticipant(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

func HandleSwitchPresenter(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin")
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.SwitchPresenterReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	m := models.NewUserModel(nil, nil, nil)
	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)
	err = m.SwitchPresenter(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
