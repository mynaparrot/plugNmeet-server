package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"google.golang.org/protobuf/proto"
)

// UserController holds dependencies for user-related handlers.
type UserController struct {
	AppConfig   *config.AppConfig
	UserModel   *models.UserModel
	ds          *dbservice.DatabaseService
	NatsService *natsservice.NatsService
}

// NewUserController creates a new UserController.
func NewUserController(appConfig *config.AppConfig, ds *dbservice.DatabaseService, natsService *natsservice.NatsService, userModel *models.UserModel) *UserController {
	return &UserController{
		AppConfig:   appConfig,
		UserModel:   userModel,
		ds:          ds,
		NatsService: natsService,
	}
}

// HandleGenerateJoinToken handles generating a join token for a user.
func (uc *UserController) HandleGenerateJoinToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GenerateTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonProtoJsonResponse(c, false, err.Error())
	}

	if req.UserInfo == nil {
		return utils.SendCommonProtoJsonResponse(c, false, "UserInfo required")
	}

	// don't generate token if user is blocked
	exist := uc.NatsService.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return utils.SendCommonProtoJsonResponse(c, false, "this user is blocked to join this session")
	}

	ri, _ := uc.ds.GetRoomInfoByRoomId(req.RoomId, 1)
	if ri == nil || ri.ID == 0 {
		return utils.SendCommonProtoJsonResponse(c, false, "room is not active. create room first")
	}

	token, err := uc.UserModel.GetPNMJoinToken(c.UserContext(), req)
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

// HandleUpdateUserLockSetting handles updating a user's lock settings.
func (uc *UserController) HandleUpdateUserLockSetting(c *fiber.Ctx) error {
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
	isRunning := 1
	room, _ := uc.ds.GetRoomInfoBySid(req.RoomSid, &isRunning)

	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	err = uc.UserModel.UpdateUserLockSettings(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleMuteUnMuteTrack handles muting or unmuting a user's track.
func (uc *UserController) HandleMuteUnMuteTrack(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	requestedUserId := c.Locals("requestedUserId")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := uc.UserModel.CommonValidation(c)
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
	room, _ := uc.ds.GetRoomInfoBySid(req.Sid, &isRunning)
	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	req.RequestedUserId = requestedUserId.(string)
	err = uc.UserModel.MuteUnMuteTrack(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleRemoveParticipant handles removing a participant from a room.
func (uc *UserController) HandleRemoveParticipant(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	err := uc.UserModel.CommonValidation(c)
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
	room, _ := uc.ds.GetRoomInfoBySid(req.Sid, &isRunning)
	if room == nil || room.ID == 0 {
		return utils.SendCommonProtobufResponse(c, false, "room isn't running")
	}

	err = uc.UserModel.RemoveParticipant(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}

// HandleSwitchPresenter handles switching the presenter in a room.
func (uc *UserController) HandleSwitchPresenter(c *fiber.Ctx) error {
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

	req.RoomId = roomId.(string)
	req.RequestedUserId = requestedUserId.(string)
	err = uc.UserModel.SwitchPresenter(req)
	if err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
