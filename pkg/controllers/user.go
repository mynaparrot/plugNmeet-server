package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/db"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// UserController holds dependencies for user-related handlers.
type UserController struct {
	AppConfig   *config.AppConfig
	UserModel   *models.UserModel
	RoomModel   *models.RoomModel
	ds          *dbservice.DatabaseService
	NatsService *natsservice.NatsService
}

type UserControllerArgs struct {
	fx.In
	AppConfig   *config.AppConfig
	UserModel   *models.UserModel
	RoomModel   *models.RoomModel
	Ds          *dbservice.DatabaseService
	NatsService *natsservice.NatsService
}

// NewUserController creates a new UserController.
func NewUserController(args UserControllerArgs) *UserController {
	return &UserController{
		AppConfig:   args.AppConfig,
		UserModel:   args.UserModel,
		RoomModel:   args.RoomModel,
		ds:          args.Ds,
		NatsService: args.NatsService,
	}
}

// HandleGenerateJoinToken handles generating a join token for a user.
func (uc *UserController) HandleGenerateJoinToken(c fiber.Ctx) error {
	req := new(plugnmeet.GenerateTokenReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrUserInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if req.UserInfo == nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrUserInfoRequired.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	// don't generate token if user is blocked
	exist := uc.NatsService.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrAuthUserBlocked.Error()), plugnmeet.StatusCode_USER_BLOCKED)
	}

	// Use the single point of truth to check if the room is active.
	rr, _, _ := uc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: req.RoomId,
	})

	if !rr.GetIsActive() {
		// the room either does not exist or is not running; the status code
		// stays accurate for both cases
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomNotActive.Error()), rr.GetStatusCode())
	}

	statusCode, token, err := uc.UserModel.GetPNMJoinToken(c.RequestCtx(), req, false)
	if err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, err.Error()), statusCode)
	}

	r := &plugnmeet.GenerateTokenRes{
		Status:     true,
		Msg:        callerMsg(c, config.MsgSuccess),
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Token:      &token,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleUpdateUserLockSetting handles updating a user's lock settings.
func (uc *UserController) HandleUpdateUserLockSetting(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")
	roomSid := fiber.Locals[string](c, "roomSid")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.UpdateUserLockSettingsReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if roomId != req.RoomId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomIdMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}
	if req.RoomSid != roomSid {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomSidMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	// now need to check if meeting is running or not
	room, _ := uc.ds.GetRoomInfoBySid(roomSid, new(1))
	if room == nil || room.ID == 0 {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomNotRunning.Error(), plugnmeet.StatusCode_ROOM_NOT_ACTIVE)
	}

	req.RequestedUserId = requestedUserId
	if statusCode, err := uc.UserModel.UpdateUserLockSettings(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleMuteUnMuteTrack handles muting or unmuting a user's track.
func (uc *UserController) HandleMuteUnMuteTrack(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.MuteUnMuteTrackReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if roomId != req.RoomId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomIdMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	// always trust the token scope over any client-supplied value
	req.Sid = fiber.Locals[string](c, "roomSid")

	// now need to check if meeting is running or not
	room, _ := uc.ds.GetRoomInfoBySid(req.Sid, new(1))
	if room == nil || room.ID == 0 {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomNotRunning.Error(), plugnmeet.StatusCode_ROOM_NOT_ACTIVE)
	}

	req.RequestedUserId = requestedUserId
	if statusCode, err := uc.UserModel.MuteUnMuteTrack(c.RequestCtx(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleRemoveParticipant handles removing a participant from a room.
func (uc *UserController) HandleRemoveParticipant(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.RemoveParticipantReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if roomId != req.RoomId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomIdMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}
	if requestedUserId == req.UserId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserCannotRemoveSelf.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	// always trust the token scope over any client-supplied value
	req.Sid = fiber.Locals[string](c, "roomSid")

	// now need to check if meeting is running or not
	room, _ := uc.ds.GetRoomInfoBySid(req.Sid, new(1))
	if room == nil || room.ID == 0 {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserRoomNotRunning.Error(), plugnmeet.StatusCode_ROOM_NOT_ACTIVE)
	}

	if statusCode, err := uc.UserModel.RemoveParticipant(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleSwitchPresenter handles switching the presenter in a room.
func (uc *UserController) HandleSwitchPresenter(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.SwitchPresenterReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrUserInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	req.RequestedUserId = requestedUserId
	if statusCode, err := uc.UserModel.SwitchPresenter(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}
