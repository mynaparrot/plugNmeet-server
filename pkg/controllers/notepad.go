package controllers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
)

// NotepadController holds dependencies for shared notepad handlers.
type NotepadController struct {
	NotepadModel *models.NotepadModel
}

type NotepadControllerArgs struct {
	fx.In
	NotepadModel *models.NotepadModel
}

func NewNotepadController(args NotepadControllerArgs) *NotepadController {
	return &NotepadController{
		NotepadModel: args.NotepadModel,
	}
}

// HandleChangeEtherpadStatus handles enabling/disabling the shared notepad.
// The route/message names are kept for API compatibility with the previous Etherpad notepad.
func (nc *NotepadController) HandleChangeEtherpadStatus(c fiber.Ctx) error {
	isAdmin := fiber.Locals[bool](c, "isAdmin")
	if !isAdmin {
		return utils.SendCommonProtobufResponse(c, false, "only admin can perform this task")
	}

	req := new(plugnmeet.ChangeEtherpadStatusReq)
	if err := proto.Unmarshal(c.Body(), req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	req.RoomId = fiber.Locals[string](c, "roomId")

	if err := nc.NotepadModel.ChangeEtherpadStatus(req); err != nil {
		return utils.SendCommonProtobufResponse(c, false, err.Error())
	}

	return utils.SendCommonProtobufResponse(c, true, "success")
}
