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
	"google.golang.org/protobuf/encoding/protojson"
	"net/url"
	"strings"
	"time"
)

func HandleVerifyApiRequest(c *fiber.Ctx) error {
	apiKey := c.Params("apiKey")
	if apiKey == "" || apiKey != config.AppCnf.Client.ApiKey {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "apiKeyError", "invalid api key"))
	}

	hasParams := false
	var data, method, rType string
	if c.Method() == "GET" {
		if len(c.Queries()) > 0 {
			rType = "get"
			hasParams = true
		}
	} else if c.Method() == "POST" {
		if c.Get("Content-Type") == "application/x-www-form-urlencoded" {
			if len(c.Body()) > 0 {
				hasParams = true
				rType = "post"
			}
		} else {
			// probably xml for Pre-upload Slides
			if len(c.Queries()) > 0 {
				hasParams = true
				rType = "get"
			}
		}
	} else {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "unsupported http method"))
	}

	if !hasParams || (strings.HasSuffix(c.Path(), "/api") || strings.HasSuffix(c.Path(), "/api/")) {
		return c.XML(bbbapiwrapper.CommonApiVersion{
			ReturnCode: "SUCCESS",
			Version:    0.9,
		})
	}

	if rType == "post" {
		s2 := strings.Split(c.Path(), "/")
		method = s2[len(s2)-1]
		data = string(c.Body())
	} else {
		s1 := strings.Split(c.OriginalURL(), "?")
		s2 := strings.Split(c.Path(), "/")
		method = s2[len(s2)-1]
		data = s1[1]
	}

	s3 := strings.Split(data, "checksum=")
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
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	// now we'll check if any presentation file was sent or not
	if c.Method() == "POST" && c.Get("Content-Type") != "application/x-www-form-urlencoded" && len(c.Body()) > 0 {
		b := new(bbbapiwrapper.PreUploadWhiteboardPostFile)
		err = xml.Unmarshal(c.Body(), b)
		if err != nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", err.Error()))
		}
		if len(b.Module.Documents) > 0 {
			for i := 0; i < len(b.Module.Documents); i++ {
				doc := b.Module.Documents[i]
				if doc.URL != "" {
					_, err := url.Parse(doc.URL)
					if err == nil {
						// we'll only accept one file
						q.PreUploadedPresentation = doc.URL
						continue
					}
				}
			}
		}
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
		MeetingID:         q.MeetingID,
		InternalMeetingID: room.Sid,
		ParentMeetingID:   "bbb-none",
		AttendeePW:        q.AttendeePW,
		ModeratorPW:       q.ModeratorPW,
		CreateTime:        room.GetCreationTime() * 1000,
		CreateDate:        time.Unix(room.GetCreationTime(), 0).Format(time.RFC1123),
		VoiceBridge:       q.VoiceBridge,
		DialNumber:        q.DialNumber,
	})
}

func HandleBBBJoin(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.JoinMeetingReq)
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
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

	roomMetadata, err := rs.UnmarshalRoomMetadata(metadata[roomId])
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	ex := new(bbbapiwrapper.CreateMeetingDefaultExtraData)
	customDesign := new(plugnmeet.CustomDesignParams)
	if roomMetadata.ExtraData != nil {
		err = json.Unmarshal([]byte(*roomMetadata.ExtraData), ex)
		if err != nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
		}
		if ex.Logo != "" {
			customDesign.CustomLogo = &ex.Logo
		}
		styleUrl := c.Query("userdata-bbb_custom_style_url")
		if styleUrl != "" {
			customDesign.CustomCssUrl = &styleUrl
		}
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
		if roomMetadata.ExtraData == nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "did not found extra data"))
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

	url := fmt.Sprintf("%s://%s/?access_token=%s", c.Protocol(), c.Hostname(), token)
	if customDesign != nil && customDesign.String() != "" {
		op := protojson.MarshalOptions{
			EmitUnpopulated: false,
			UseProtoNames:   true,
		}
		cd, err := op.Marshal(customDesign)
		if err != nil {
			return err
		}
		url = fmt.Sprintf("%s&custom_design=%s", url, string(cd))
	}

	if strings.ToLower(q.Redirect) == "false" {
		return c.XML(bbbapiwrapper.JoinMeetingRes{
			ReturnCode:   "SUCCESS",
			MessageKey:   "success",
			Message:      "You have joined successfully",
			MeetingID:    q.MeetingID,
			SessionToken: token,
			Url:          url,
		})
	}

	return c.Redirect(url)
}

func HandleBBBIsMeetingRunning(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
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
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRoomAuthModel()
	status, msg, res := m.GetActiveRoomInfo(&plugnmeet.GetActiveRoomInfoReq{
		RoomId: bbbapiwrapper.CheckMeetingIdToMatchFormat(q.MeetingID),
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
	_, _, rooms := m.GetActiveRoomsInfo()

	if rooms == nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("SUCCESS", "noMeetings", "no meetings were found on this server"))
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
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	m := models.NewRoomAuthModel()
	status, msg := m.EndRoom(&plugnmeet.RoomEndReq{
		RoomId: bbbapiwrapper.CheckMeetingIdToMatchFormat(q.MeetingID),
	})

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", msg))
	}
	return c.XML(bbbapiwrapper.CommonResponseMsg("SUCCESS", "sentEndMeetingRequest", "A request to end the meeting was sent.  Please wait a few seconds, and then use the getMeetingInfo or isMeetingRunning API calls to verify that it was ended"))
}

func HandleBBBGetRecordings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.GetRecordingsReq)
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
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
	var err error = nil
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
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
