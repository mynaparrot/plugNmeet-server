package controllers

import (
	"encoding/json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	"github.com/mynaparrot/plugNmeet/internal/models"
	"strings"
)

func HandleCreatePoll(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin")

	if !isAdmin.(bool) {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this task",
		})
	}

	m := models.NewPollsModel()
	req := new(models.CreatePollReq)

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	req.RoomId = roomId.(string)
	err = m.CreatePoll(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}

func HandleListPolls(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	m := models.NewPollsModel()
	err, polls := m.ListPolls(roomId.(string))
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"polls":  polls,
	})
}

func HandleCountPollTotalResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")

	if pollId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "pollId required",
		})
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponsesByField(roomId.(string), pollId, "total_resp")

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":          true,
		"msg":             "success",
		"poll_id":         pollId,
		"total_responses": responses,
	})
}

func HandleUserSelectedOption(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")
	userId := c.Params("userId")

	if pollId == "" || userId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "both userId & pollId required",
		})
	}

	m := models.NewPollsModel()
	err, allRespondents := m.GetPollResponsesByField(roomId.(string), pollId, "all_respondents")

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	if allRespondents == "" {
		return c.JSON(fiber.Map{
			"status":  true,
			"msg":     "success",
			"poll_id": pollId,
			"voted":   0,
		})
	}

	var respondents []string
	err = json.Unmarshal([]byte(allRespondents), &respondents)
	if err != nil {
		return err
	}

	for i := 0; i < len(respondents); i++ {
		p := strings.Split(respondents[i], ":")
		if p[0] == userId {
			return c.JSON(fiber.Map{
				"status":  true,
				"msg":     "success",
				"poll_id": pollId,
				"voted":   p[1],
			})
		}
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": pollId,
		"voted":   0,
	})
}

func HandlePollResponses(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	pollId := c.Params("pollId")

	if pollId == "" {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "pollId required",
		})
	}

	m := models.NewPollsModel()
	err, responses := m.GetPollResponses(roomId.(string), pollId)

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":    true,
		"msg":       "success",
		"poll_id":   pollId,
		"responses": responses,
	})
}

func HandleUserSubmitResponse(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	m := models.NewPollsModel()
	req := new(models.UserSubmitResponseReq)

	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	check := config.AppCnf.DoValidateReq(req)
	if len(check) > 0 {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    check,
		})
	}

	req.RoomId = roomId.(string)
	err = m.UserSubmitResponse(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status":  true,
		"msg":     "success",
		"poll_id": req.PollId,
	})
}
