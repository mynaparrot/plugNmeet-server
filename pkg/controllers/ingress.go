package controllers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"google.golang.org/protobuf/proto"
)

func HandleCreateIngress(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")
	res := new(plugnmeet.CreateIngressRes)
	res.Status = false

	if !isAdmin.(bool) {
		res.Msg = "only admin can perform this task"
		return SendCreateIngressResponse(c, res)
	}

	req := new(plugnmeet.CreateIngressReq)
	err := proto.Unmarshal(c.Body(), req)
	if err != nil {
		res.Msg = err.Error()
		return SendCreateIngressResponse(c, res)
	}

	m := models.NewIngressModel()
	req.RoomId = roomId.(string)
	f, err := m.CreateIngress(req)
	if err != nil {
		res.Msg = err.Error()
		return SendCreateIngressResponse(c, res)
	}

	res.Status = true
	res.Msg = "success"
	res.Url = f.Url
	res.StreamKey = f.StreamKey

	return SendCreateIngressResponse(c, res)
}

func SendCreateIngressResponse(c *fiber.Ctx, res *plugnmeet.CreateIngressRes) error {
	marshal, err := proto.Marshal(res)
	if err != nil {
		return err
	}
	c.Set("Content-Type", "application/protobuf")
	return c.Send(marshal)
}
