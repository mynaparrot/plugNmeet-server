package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

// IngressController holds dependencies for ingress-related handlers.
type IngressController struct {
	IngressModel *models.IngressModel
}

// NewIngressController creates a new IngressController.
func NewIngressController(im *models.IngressModel) *IngressController {
	return &IngressController{
		IngressModel: im,
	}
}

// HandleCreateIngress handles creating a new ingress.
func (ic *IngressController) HandleCreateIngress(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.CreateIngressRes)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return sendCreateIngressResponse(c, res)
	}

	req := new(plugnmeet.CreateIngressReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return sendCreateIngressResponse(c, res)
	}

	req.RoomId = roomId.(string)
	f, err := ic.IngressModel.CreateIngress(req)
	if err != nil {
		res.Msg = err.Error()
		return sendCreateIngressResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Url = f.Url
	res.StreamKey = f.StreamKey

	return sendCreateIngressResponse(c, res)
}

func sendCreateIngressResponse(c *fiber.Ctx, res *plugnmeet.CreateIngressRes) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
