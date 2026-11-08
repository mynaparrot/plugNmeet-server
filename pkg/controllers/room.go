package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// RoomController holds dependencies for room-related handlers.
type RoomController struct {
	RoomModel *models.RoomModel
}

type RoomControllerArgs struct {
	fx.In
	RoomModel *models.RoomModel
}

// NewRoomController creates a new RoomController.
func NewRoomController(args RoomControllerArgs) *RoomController {
	return &RoomController{
		RoomModel: args.RoomModel,
	}
}

// HandleRoomCreate handles creating a new room.
func (rc *RoomController) HandleRoomCreate(c fiber.Ctx) error {
	req := new(plugnmeet.CreateRoomReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	statusCode, room, err := rc.RoomModel.CreateRoom(c.RequestCtx(), req)
	if err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, err.Error()), statusCode)
	}

	r := &plugnmeet.CreateRoomRes{
		Status:     true,
		Msg:        callerMsg(c, config.MsgSuccess),
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		RoomInfo:   room,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleIsRoomActive checks if a room is active.
func (rc *RoomController) HandleIsRoomActive(c fiber.Ctx) error {
	req := new(plugnmeet.IsRoomActiveReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	res, _, _ := rc.RoomModel.IsRoomActive(req)
	if !res.GetStatus() {
		// a room that exists but is not active is a normal answer, not a failure
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, res.GetMsg()), res.GetStatusCode())
	}
	// the model answers with the client success key, integrators get plain English
	res.Msg = callerMsg(c, res.GetMsg())
	return utils.SendProtoJsonResponse(c, res)
}

// HandleGetActiveRoomInfo gets information about an active room.
func (rc *RoomController) HandleGetActiveRoomInfo(c fiber.Ctx) error {
	req := new(plugnmeet.GetActiveRoomInfoReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	status, msg, statusCode, res := rc.RoomModel.GetActiveRoomInfo(c.RequestCtx(), req)
	if !status {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, msg), statusCode)
	}

	r := &plugnmeet.GetActiveRoomInfoRes{
		Status:     status,
		Msg:        callerMsg(c, msg),
		StatusCode: statusCode,
		Room:       res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleGetActiveRoomsInfo gets information about all active rooms.
func (rc *RoomController) HandleGetActiveRoomsInfo(c fiber.Ctx) error {
	status, msg, statusCode, res := rc.RoomModel.GetActiveRoomsInfo(c.RequestCtx())
	if !status {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, msg), statusCode)
	}

	r := &plugnmeet.GetActiveRoomsInfoRes{
		Status:     status,
		Msg:        callerMsg(c, msg),
		StatusCode: statusCode,
		Rooms:      res,
	}

	return utils.SendProtoJsonResponse(c, r)
}

// HandleEndRoom handles ending a room.
func (rc *RoomController) HandleEndRoom(c fiber.Ctx) error {
	req := new(plugnmeet.RoomEndReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	status, msg, statusCode := rc.RoomModel.EndRoom(c.RequestCtx(), req)
	if !status {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, msg), statusCode)
	}
	return utils.SendCommonProtoJsonResponse(c, status, callerMsg(c, msg), statusCode)
}

// HandleFetchPastRooms handles fetching past rooms.
func (rc *RoomController) HandleFetchPastRooms(c fiber.Ctx) error {
	req := new(plugnmeet.FetchPastRoomsReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	statusCode, result, err := rc.RoomModel.FetchPastRooms(c.RequestCtx(), req)
	if err != nil {
		// the model already logged the operational error
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, err.Error()), statusCode)
	}

	r := &plugnmeet.FetchPastRoomsRes{
		Status:     true,
		Msg:        callerMsg(c, config.MsgSuccess),
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		Result:     result,
	}
	return utils.SendProtoJsonResponse(c, r)
}

// HandleBroadcastToRoom handles broadcasting message/notificaiton to a room.
func (rc *RoomController) HandleBroadcastToRoom(c fiber.Ctx) error {
	req := new(plugnmeet.BroadcastToRoomReq)
	if err := parseAndValidateRequest(c.Body(), req); err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, config.ErrRoomInvalidRequest.Error()), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	res, _, _ := rc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{RoomId: req.RoomId})
	if !res.GetIsActive() {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, res.GetMsg()), res.GetStatusCode())
	}

	statusCode, err := rc.RoomModel.BroadcastToRoom(req)
	if err != nil {
		return utils.SendCommonErrorJsonResponse(c, callerMsg(c, err.Error()), statusCode)
	}

	return utils.SendCommonProtoJsonResponse(c, true, callerMsg(c, config.MsgSuccess), plugnmeet.StatusCode_SUCCESS)
}

// HandleEndRoomForAPI handles ending a room via API call.
func (rc *RoomController) HandleEndRoomForAPI(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.RoomEndReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if roomId != req.RoomId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomIdMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	status, msg, statusCode := rc.RoomModel.EndRoom(c.RequestCtx(), req)
	if !status {
		return utils.SendCommonErrorProtobufResponse(c, msg, statusCode)
	}
	return utils.SendCommonProtobufResponse(c, true, msg)
}

// HandleChangeVisibilityForAPI handles changing room visibility via API call.
func (rc *RoomController) HandleChangeVisibilityForAPI(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.ChangeVisibilityRes)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	if roomId != req.RoomId {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomIdMismatch.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	statusCode, err := rc.RoomModel.ChangeVisibility(req)
	if err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

func (rc *RoomController) HandleEnableRoomSipDialIn(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.EnableSipDialInReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}
	req.RoomId = roomId

	if statusCode, err := rc.RoomModel.EnableRoomSipDialIn(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}
	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

func (rc *RoomController) HandleCreateIngress(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.CreateIngressReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	statusCode, f, err := rc.RoomModel.CreateIngress(req)
	if err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	res := &plugnmeet.CreateIngressRes{
		Status:    true,
		Msg:       config.MsgSuccess,
		Url:       f.Url,
		StreamKey: f.StreamKey,
	}

	return utils.SendProtobufResponse(c, res)
}

// HandleExternalDisplayLink handles sharing an external display link.
func (rc *RoomController) HandleExternalDisplayLink(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	if roomId == "" {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomIdRequired.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req := new(plugnmeet.ExternalDisplayLinkReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	if statusCode, err := rc.RoomModel.HandleExternalDisplayTask(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleCountdownTimer handles the room's shared countdown timer tasks.
func (rc *RoomController) HandleCountdownTimer(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrCountdownTimerOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	if roomId == "" {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrCountdownTimerGeneric.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req := new(plugnmeet.CountdownTimerReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrCountdownTimerGeneric.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	if statusCode, err := rc.RoomModel.HandleCountdownTimerTask(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleExternalMediaPlayer handles external media player actions.
func (rc *RoomController) HandleExternalMediaPlayer(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	roomId := fiber.Locals[string](c, "roomId")
	requestedUserId := fiber.Locals[string](c, "requestedUserId")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	if roomId == "" {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomIdRequired.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req := new(plugnmeet.ExternalMediaPlayerReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	req.UserId = requestedUserId
	if statusCode, err := rc.RoomModel.HandleExternalMediaTask(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleApproveUsers handles approving users from the waiting room.
func (rc *RoomController) HandleApproveUsers(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.ApproveWaitingUsersReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	if statusCode, err := rc.RoomModel.ApproveWaitingUsers(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleUpdateWaitingRoomMessage handles updating the waiting room message.
func (rc *RoomController) HandleUpdateWaitingRoomMessage(c fiber.Ctx) error {
	roomId := fiber.Locals[string](c, "roomId")
	isAdmin := fiber.Locals[bool](c, "isAdmin")

	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.UpdateWaitingRoomMessageReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = roomId
	if statusCode, err := rc.RoomModel.UpdateWaitingRoomMessage(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}

// HandleChangeSharedNotepadStatus handles enabling/disabling the shared notepad.
func (rc *RoomController) HandleChangeSharedNotepadStatus(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	if !isAdmin {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomOnlyAdmin.Error(), plugnmeet.StatusCode_PERMISSION_DENIED)
	}

	req := new(plugnmeet.ChangeSharedNotepadStatusReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, config.ErrRoomInvalidRequest.Error(), plugnmeet.StatusCode_INVALID_PARAMETERS)
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	if statusCode, err := rc.RoomModel.ChangeSharedNotepadStatus(req); err != nil {
		return utils.SendCommonErrorProtobufResponse(c, err.Error(), statusCode)
	}

	return utils.SendCommonProtobufResponse(c, true, config.MsgSuccess)
}
