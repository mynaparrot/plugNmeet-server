package controllers

import (
	"crypto/subtle"
	"encoding/xml"
	"fmt"
	"github.com/bufbuild/protovalidate-go"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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

	s3 := strings.Split(s1[1], "checksum=")
	if len(s3) < 1 {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "checksumError", "Checksums do not match"))
	}

	var queries string
	var checksum string
	// if no other query
	if len(s3) == 1 {
		checksum = s3[0]
	} else {
		checksum = s3[1]
		queries = strings.TrimSuffix(s3[0], "&")
	}

	ourSum := bbbapiwrapper.CalculateCheckSum(config.AppCnf.Client.Secret, method, queries)
	if subtle.ConstantTimeCompare([]byte(checksum), []byte(ourSum)) != 1 {
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

	pnmReq, err := bbbapiwrapper.ConvertCreateRequest(q, c.Queries())
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

func HandleBBBIsMeetingRunning(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRoomAuthModel()
	status, _, _ := m.IsRoomActive(&plugnmeet.IsRoomActiveReq{
		RoomId: q.MeetingID,
	})

	return c.XML(bbbapiwrapper.IsMeetingRunningRes{
		ReturnCode: "SUCCESS",
		Running:    status,
	})
}

func HandleBBBGetMeetingInfo(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomInfo(&plugnmeet.GetActiveRoomInfoReq{
		RoomId: q.MeetingID,
	})

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "notFound", msg))
	}

	d := bbbapiwrapper.ConvertActiveRoomInfoToBBBMeetingInfo(res)
	marshal, err := xml.Marshal(d)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	dd := strings.Replace(string(marshal), "<meeting>", "", 1)
	dd = strings.Replace(dd, "</meeting>", "", 1)

	c.Set("Content-Type", "application/xml")
	return c.SendString("<response><returncode>SUCCESS</returncode>" + dd + "</response>")
}

func HandleBBBGetMeetings(c *fiber.Ctx) error {
	m := models.NewRoomAuthModel()
	status, msg, rooms := m.GetActiveRoomsInfo()

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "noMeetings", msg))
	}

	var meetings []*bbbapiwrapper.MeetingInfo
	for _, r := range rooms {
		d := bbbapiwrapper.ConvertActiveRoomInfoToBBBMeetingInfo(r)
		meetings = append(meetings, d)
	}

	res := bbbapiwrapper.GetMeetingsRes{
		ReturnCode: "SUCCESS",
	}
	res.MeetingsInfo.Meetings = meetings
	return c.XML(res)
}

func HandleBBBEndMeetings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(&plugnmeet.RoomEndReq{
		RoomId: q.MeetingID,
	})

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", msg))
	}
	return c.XML(bbbapiwrapper.CommonResponseMsg("SUCCESS", "sentEndMeetingRequest", "A request to end the meeting was sent.  Please wait a few seconds, and then use the getMeetingInfo or isMeetingRunning API calls to verify that it was ended"))
}

func HandleBBBGetRecordings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.GetRecordingsReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	host := fmt.Sprintf("%s://%s", c.Protocol(), c.Hostname())
	m := models.NewBBBApiWrapperModel()
	recordings, pagination, err := m.GetRecordings(host, q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	if len(recordings) == 0 {
		return c.XML(bbbapiwrapper.CommonResponseMsg("SUCCESS", "noRecordings", "There are no recordings for the meeting(s)."))
	}
	res := bbbapiwrapper.GetRecordingsRes{
		ReturnCode: "SUCCESS",
		Pagination: pagination,
	}
	res.RecordingsInfo.Recordings = recordings
	return c.XML(res)
}

func HandleBBBDeleteRecordings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.DeleteRecordingsReq)
	err := c.QueryParser(q)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRecordingAuth()
	err = m.DeleteRecording(&plugnmeet.DeleteRecordingReq{
		RecordId: q.RecordID,
	})

	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	return c.XML(bbbapiwrapper.DeleteRecordingsRes{
		ReturnCode: "SUCCESS",
		Deleted:    true,
	})
}

// HandleBBBPublishRecordings TO-DO: in the future
func HandleBBBPublishRecordings(c *fiber.Ctx) error {
	return c.XML(bbbapiwrapper.PublishRecordingsRes{
		ReturnCode: "SUCCESS",
		Published:  true,
	})
}

// HandleBBBUpdateRecordings TO-DO: in the future
func HandleBBBUpdateRecordings(c *fiber.Ctx) error {
	return c.XML(bbbapiwrapper.UpdateRecordingsRes{
		ReturnCode: "SUCCESS",
		Updated:    true,
	})
}
