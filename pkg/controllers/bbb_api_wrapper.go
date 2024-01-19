package controllers

import (
	"crypto/subtle"
	"fmt"
	"github.com/bufbuild/protovalidate-go"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"strings"
	"time"
)

func HandleVerifyApiRequest(c *fiber.Ctx) error {
	apiKey := c.Params("apiKey")
	if apiKey == "" || apiKey != config.AppCnf.Client.ApiKey {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "apiKeyError", "invalid api key"))
	}

	q := c.Queries()
	if q == nil || len(q) == 0 {
		return c.XML(bbbapiwrapper.CommonApiVersion{
			ReturnCode: "SUCCESS",
			Version:    0.9,
		})
	}

	s1 := strings.Split(c.OriginalURL(), "?")
	// we'll get method
	s2 := strings.Split(s1[0], "/")
	method := s2[len(s2)-1]

	s3 := strings.Split(s1[1], "&checksum=")
	if len(s3) < 1 {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "checksumError", "Checksums do not match"))
	}
	ourSum := bbbapiwrapper.CalculateCheckSum(config.AppCnf.Client.Secret, method, s3[0])

	if subtle.ConstantTimeCompare([]byte(s3[1]), []byte(ourSum)) != 1 {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "checksumError", "Checksums do not match"))
	}

	return c.Next()
}

func HandleBBBCreate(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.CreateMeetingReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	pnmReq, err := bbbapiwrapper.ConvertCreateRequest(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	v, err := protovalidate.New()
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", "failed to initialize validator: "+err.Error()))
	}

	if err = v.Validate(pnmReq); err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", err.Error()))
	}

	m := models.NewRoomAuthModel()
	status, msg, room := m.CreateRoom(pnmReq)

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", msg))
	}

	return c.XML(bbbapiwrapper.CreateMeetingResp{
		ReturnCode:        "SUCCESS",
		MessageKey:        "success",
		Message:           msg,
		MeetingID:         room.Name,
		InternalMeetingID: room.Sid,
		AttendeePW:        q.AttendeePW,
		ModeratorPW:       q.ModeratorPW,
		CreateTime:        room.GetCreationTime() * 1000,
		CreateDate:        time.Unix(room.GetCreationTime(), 0).Format(time.RFC1123),
	})
}

func HandleBBBJoin(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.JoinMeetingReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	roomId := bbbapiwrapper.CheckMeetingIdToMatchFormat(q.MeetingID)
	rs := models.NewRoomService()
	metadata, err := rs.ManageActiveRoomsWithMetadata(roomId, "get", "")
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	if metadata == nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "meeting is not active"))
	}

	isAdmin := false
	if q.Role != "" {
		if strings.ToUpper(q.Role) == "MODERATOR" {
			isAdmin = true
		}
	} else {
		if q.Password == "" {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "password missing"))
		}

		roomMetadata, err := rs.UnmarshalRoomMetadata(metadata[roomId])
		if err != nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
		}

		if roomMetadata.ExtraData == nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "did not found extra data"))
		}

		ex := new(bbbapiwrapper.CreateMeetingDefaultExtraData)
		err = json.Unmarshal([]byte(*roomMetadata.ExtraData), ex)
		if err != nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
		}

		if subtle.ConstantTimeCompare([]byte(q.Password), []byte(ex.ModeratorPW)) == 1 {
			isAdmin = true
		}
	}

	req := bbbapiwrapper.ConvertJoinRequest(q, isAdmin)
	v, err := protovalidate.New()
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", "failed to initialize validator: "+err.Error()))
	}

	if err = v.Validate(req); err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", err.Error()))
	}

	exist := rs.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", "this user is blocked to join this session"))
	}

	m := models.NewAuthTokenModel()
	token, err := m.GeneratePlugNmeetAccessToken(req)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	url := fmt.Sprintf("/?access_token=%s", token)
	return c.Redirect(url)
}
