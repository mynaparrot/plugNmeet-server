package controllers

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-protocol/utils"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
)

const (
	// Error messages
	errAuthorizationHeaderMissing = "Authorization header is missing"
	errInvalidAuthorizationHeader = "invalid authorization header"
	errOnlyAdminCanPerform        = "only admin can perform this"
	errPleaseUsePostRequest       = "please use POST request"
	errRoomIdMissing              = "room id is missing"

	// Context keys
	ctxKeyRoomID       = "roomId"
	ctxKeyRoomTitle    = "roomTitle"
	ctxKeyUserID       = "userId"
	ctxKeyName         = "name"
	ctxKeyIsAdmin      = "isAdmin"
	ctxKeyCustomParams = "customParams"
)

// LtiV1Controller holds dependencies for LTI v1 related handlers.
type LtiV1Controller struct {
	app            *config.AppConfig
	LtiV1Model     *models.LtiV1Model
	RoomModel      *models.RoomModel
	RecordingModel *models.RecordingModel
}

// NewLtiV1Controller creates a new LtiV1Controller.
func NewLtiV1Controller(app *config.AppConfig, lm *models.LtiV1Model, rm *models.RoomModel, recm *models.RecordingModel) *LtiV1Controller {
	return &LtiV1Controller{
		app:            app,
		LtiV1Model:     lm,
		RoomModel:      rm,
		RecordingModel: recm,
	}
}

// HandleLTIV1Landing handles the initial LTI v1 landing request.
func (lc *LtiV1Controller) HandleLTIV1Landing(c *fiber.Ctx) error {
	b := c.Body()
	if len(b) == 0 {
		return c.Status(fiber.StatusUnauthorized).SendString("empty body")
	}

	hostName := c.Hostname()
	proto := "https"
	if c.Protocol() == "http" {
		proto = "http"
	}
	// fiber format: [host]:[port]
	parts := strings.Split(hostName, ":")

	// we can use BBBJoinHost to build correct info
	if lc.app.Client.BBBJoinHost != nil && *lc.app.Client.BBBJoinHost != "" {
		if u, err := url.Parse(*lc.app.Client.BBBJoinHost); err == nil {
			hostName = u.Hostname()
			proto = u.Scheme

			if len(parts) == 2 {
				// we'll use port from the request
				hostName = fmt.Sprintf("%s:%s", hostName, parts[1])
			}
		}
	}

	signingURL := fmt.Sprintf("%s://%s%s", proto, hostName, c.Path())

	return lc.LtiV1Model.LTIV1Landing(c, string(b), signingURL)
}

// HandleLTIV1GETREQUEST handles GET requests to LTI endpoints, which are not allowed.
func (lc *LtiV1Controller) HandleLTIV1GETREQUEST(c *fiber.Ctx) error {
	return sendErrorResponse(c, fiber.StatusMethodNotAllowed, errPleaseUsePostRequest)
}

// HandleLTIV1VerifyHeaderToken is a middleware to verify the LTI Authorization header token.
func (lc *LtiV1Controller) HandleLTIV1VerifyHeaderToken(c *fiber.Ctx) error {
	authToken := c.Get("Authorization")
	if authToken == "" {
		return sendErrorResponse(c, fiber.StatusUnauthorized, errAuthorizationHeaderMissing)
	}

	auth, err := lc.LtiV1Model.LTIV1VerifyHeaderToken(authToken)
	if err != nil {
		return sendErrorResponse(c, fiber.StatusUnauthorized, errInvalidAuthorizationHeader)
	}

	c.Locals(ctxKeyRoomID, auth.RoomId)
	c.Locals(ctxKeyRoomTitle, auth.RoomTitle)
	c.Locals(ctxKeyUserID, auth.UserId)
	c.Locals(ctxKeyName, auth.Name)
	c.Locals(ctxKeyIsAdmin, auth.IsAdmin)

	if auth.LtiCustomParameters != nil {
		customParams, err := json.Marshal(auth.LtiCustomParameters)
		if err == nil {
			c.Locals(ctxKeyCustomParams, customParams)
		}
	}

	return c.Next()
}

// HandleLTIV1IsRoomActive checks if the LTI room is active.
func (lc *LtiV1Controller) HandleLTIV1IsRoomActive(c *fiber.Ctx) error {
	roomId, ok := c.Locals(ctxKeyRoomID).(string)
	if !ok {
		return sendErrorResponse(c, fiber.StatusBadRequest, errRoomIdMissing)
	}

	res, _, _ := lc.RoomModel.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: roomId,
	})

	return utils.SendProtoJsonResponse(c, res)
}

// HandleLTIV1JoinRoom handles joining an LTI room.
func (lc *LtiV1Controller) HandleLTIV1JoinRoom(c *fiber.Ctx) error {
	claim := &plugnmeet.LtiClaims{
		UserId:    c.Locals(ctxKeyUserID).(string),
		Name:      c.Locals(ctxKeyName).(string),
		IsAdmin:   c.Locals(ctxKeyIsAdmin).(bool),
		RoomId:    c.Locals(ctxKeyRoomID).(string),
		RoomTitle: c.Locals(ctxKeyRoomTitle).(string),
	}

	if customParams, ok := c.Locals(ctxKeyCustomParams).([]byte); ok && len(customParams) > 0 {
		p := new(plugnmeet.LtiCustomParameters)
		if err := json.Unmarshal(customParams, p); err == nil {
			claim.LtiCustomParameters = p
		}
	}

	token, err := lc.LtiV1Model.LTIV1JoinRoom(c.UserContext(), claim)
	if err != nil {
		return sendErrorResponse(c, fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

// HandleLTIV1EndRoom handles ending an LTI room session.
func (lc *LtiV1Controller) HandleLTIV1EndRoom(c *fiber.Ctx) error {
	if isAdmin, ok := c.Locals(ctxKeyIsAdmin).(bool); !ok || !isAdmin {
		return sendErrorResponse(c, fiber.StatusForbidden, errOnlyAdminCanPerform)
	}

	roomId, ok := c.Locals(ctxKeyRoomID).(string)
	if !ok {
		return sendErrorResponse(c, fiber.StatusBadRequest, errRoomIdMissing)
	}
	status, msg := lc.RoomModel.EndRoom(c.UserContext(), &plugnmeet.RoomEndReq{
		RoomId: roomId,
	})

	return c.JSON(fiber.Map{
		"status": status,
		"msg":    msg,
	})
}

// HandleLTIV1FetchRecordings fetches recordings for an LTI room.
func (lc *LtiV1Controller) HandleLTIV1FetchRecordings(c *fiber.Ctx) error {
	req := new(plugnmeet.FetchRecordingsReq)
	if err := c.BodyParser(req); err != nil {
		return sendErrorResponse(c, fiber.StatusBadRequest, err.Error())
	}

	roomId, ok := c.Locals(ctxKeyRoomID).(string)
	if !ok {
		return sendErrorResponse(c, fiber.StatusBadRequest, errRoomIdMissing)
	}
	req.RoomIds = []string{roomId}
	result, err := lc.RecordingModel.FetchRecordings(req)
	if err != nil {
		return sendErrorResponse(c, fiber.StatusInternalServerError, err.Error())
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
	if err := c.BodyParser(req); err != nil {
		return sendErrorResponse(c, fiber.StatusBadRequest, err.Error())
	}

	token, err := lc.RecordingModel.GetDownloadToken(req)
	if err != nil {
		return sendErrorResponse(c, fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
		"token":  token,
	})
}

// HandleLTIV1DeleteRecordings deletes a recording for an LTI room.
func (lc *LtiV1Controller) HandleLTIV1DeleteRecordings(c *fiber.Ctx) error {
	if isAdmin, ok := c.Locals(ctxKeyIsAdmin).(bool); !ok || !isAdmin {
		return sendErrorResponse(c, fiber.StatusForbidden, errOnlyAdminCanPerform)
	}

	req := new(plugnmeet.DeleteRecordingReq)
	if err := c.BodyParser(req); err != nil {
		return sendErrorResponse(c, fiber.StatusBadRequest, err.Error())
	}

	if err := lc.RecordingModel.DeleteRecording(req); err != nil {
		return sendErrorResponse(c, fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{
		"status": true,
		"msg":    "success",
	})
}

func sendErrorResponse(c *fiber.Ctx, code int, msg string) error {
	return c.Status(code).JSON(fiber.Map{
		"status": false,
		"msg":    msg,
	})
}
