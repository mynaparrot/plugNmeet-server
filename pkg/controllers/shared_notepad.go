package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// SharedNotepadController holds dependencies for shared notepad handlers.
type SharedNotepadController struct {
	NotepadModel *models.SharedNotepadModel
}

type SharedNotepadControllerArgs struct {
	fx.In
	NotepadModel *models.SharedNotepadModel
}

func NewSharedNotepadController(args SharedNotepadControllerArgs) *SharedNotepadController {
	return &SharedNotepadController{
		NotepadModel: args.NotepadModel,
	}
}

// HandleChangeSharedNotepadStatus handles enabling/disabling the shared notepad.
// The route/message names are kept for API compatibility with the previous Etherpad notepad.
func (nc *SharedNotepadController) HandleChangeSharedNotepadStatus(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeSharedNotepadStatusReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = fiber.Locals[string](c, "roomId")
	if err := nc.NotepadModel.ChangeSharedNotepadStatus(req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
