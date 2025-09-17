package controllers

import (
	"fmt"
	"strings"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

// LtiV1Controller holds dependencies for LTI v1 related handlers.
type LtiV1Controller struct {
	LtiV1Model     *models.LtiV1Model
	RoomModel      *models.RoomModel
	RecordingModel *models.RecordingModel
}

// NewLtiV1Controller creates a new LtiV1Controller.
func NewLtiV1Controller(lm *models.LtiV1Model, rm *models.RoomModel, recm *models.RecordingModel) *LtiV1Controller {
	return &LtiV1Controller{
		LtiV1Model:     lm,
		RoomModel:      rm,
		RecordingModel: recm,
	}
}

// HandleLTIV1Landing handles the initial LTI v1 landing request.
func (lc *LtiV1Controller) HandleLTIV1Landing(c *fiber.Ctx) error {
	b := make([]byte, len(c.Body()))
	copy(b, c.Body())

	if len(b) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("empty body")
	}

	proto := "https"
	if strings.Contains(c.Hostname(), "localhost") {
		proto = "http"
	}
	signingURL := fmt.Sprintf("%s://%s%s", proto, c.Hostname(), c.Path())

	err := lc.LtiV1Model.LTIV1Landing(c, string(b), signingURL)
	if err != nil {
		return err
	}

	return nil
}

// HandleLTIV1GETREQUEST handles GET requests to LTI endpoints, which are not allowed.
func (lc *LtiV1Controller) HandleLTIV1GETREQUEST(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"status": false,
		"msg":    "please use POST request",
	})
}

// HandleLTIV1VerifyHeaderToken is a middleware to verify the LTI Authorization header token.
func (lc *LtiV1Controller) HandleLTIV1VerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	if authToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "Authorization header is missing",
		})
	}

	auth, err := lc.LtiV1Model.LTIV1VerifyHeaderToken(authToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"msg":    "invalid authorization header",
		})
	}

	c.Locals("roomId", auth.RoomId)
	c.Locals("roomTitle", auth.RoomTitle)

	c.Locals("userId", auth.UserId)
	c.Locals("name", auth.Name)
	c.Locals("isAdmin", auth.IsAdmin)

	if auth.LtiCustomParameters != nil {
		customParams, err := json.Marshal(auth.LtiCustomParameters)
		if err == nil {
			c.Locals("customParams", customParams)
		}
	}

	return c.Next()
}

// HandleLTIV1IsRoomActive checks if the LTI room is active.
func (lc *LtiV1Controller) HandleLTIV1IsRoomActive(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	res, _, _, _ := lc.RoomModel.IsRoomActive(c.UserContext(), &plugnmeet.IsRoomActiveReq{
		RoomId: roomId.(string),
	})

	return utils.SendProtoJsonResponse(c, res)
}

// HandleLTIV1JoinRoom handles joining an LTI room.
func (lc *LtiV1Controller) HandleLTIV1JoinRoom(c *fiber.Ctx) error {
	customParams := c.Locals("customParams").([]byte)

	claim := &plugnmeet.LtiClaims{
		UserId:    c.Locals("userId").(string),
		Name:      c.Locals("name").(string),
		IsAdmin:   c.Locals("isAdmin").(bool),
		RoomId:    c.Locals("roomId").(string),
		RoomTitle: c.Locals("roomTitle").(string),
	}

	if len(customParams) > 0 {
		p := new(plugnmeet.LtiCustomParameters)
		err := json.Unmarshal(customParams, p)
		if err == nil {
			claim.LtiCustomParameters = p
		}
	}

	token, err := lc.LtiV1Model.LTIV1JoinRoom(c.UserContext(), claim)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

// HandleLTIV1EndRoom handles ending an LTI room session.
func (lc *LtiV1Controller) HandleLTIV1EndRoom(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	isAdmin := c.Locals("isAdmin").(bool)

	if !isAdmin {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this",
		})
	}

	status, msg := lc.RoomModel.EndRoom(c.UserContext(), &plugnmeet.RoomEndReq{
		RoomId: roomId.(string),
	})

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}

// HandleLTIV1FetchRecordings fetches recordings for an LTI room.
func (lc *LtiV1Controller) HandleLTIV1FetchRecordings(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")

	req := new(plugnmeet.FetchRecordingsReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	req.RoomIds = []string{roomId.(string)}
	result, err := lc.RecordingModel.FetchRecordings(req)

	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"result": result,
	})
}

// HandleLTIV1GetRecordingDownloadToken gets a download token for a recording.
func (lc *LtiV1Controller) HandleLTIV1GetRecordingDownloadToken(c *fiber.Ctx) error {
	req := new(plugnmeet.GetDownloadTokenReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	token, err := lc.RecordingModel.GetDownloadToken(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

// HandleLTIV1DeleteRecordings deletes a recording for an LTI room.
func (lc *LtiV1Controller) HandleLTIV1DeleteRecordings(c *fiber.Ctx) error {
	isAdmin := c.Locals("isAdmin").(bool)

	if !isAdmin {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    "only admin can perform this",
		})
	}

	req := new(plugnmeet.DeleteRecordingReq)
	err := c.BodyParser(req)
	if err != nil {
		return c.JSON(fiber.Map{
			"status": false,
			"msg":    err.Error(),
		})
	}

	err = lc.RecordingModel.DeleteRecording(req)
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
