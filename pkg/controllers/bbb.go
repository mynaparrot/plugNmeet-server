package controllers

import (
	"crypto/subtle"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	natsservice "github.com/mynaparrot/plugnmeet-server/pkg/services/nats"
	"google.golang.org/protobuf/encoding/protojson"
)

// BBBController holds dependencies for BBB API compatibility handlers.
type BBBController struct {
	AppConfig          *config.AppConfig
	RoomModel          *models.RoomModel
	UserModel          *models.UserModel
	BBBApiWrapperModel *models.BBBApiWrapperModel
	RecordingModel     *models.RecordingModel
	NatsService        *natsservice.NatsService
}

// NewBBBController creates a new BBBController.
func NewBBBController(config *config.AppConfig, roomModel *models.RoomModel, userModel *models.UserModel, bbbApiWrapperModel *models.BBBApiWrapperModel, recordingModel *models.RecordingModel, natsService *natsservice.NatsService) *BBBController {
	return &BBBController{
		AppConfig:          config,
		RoomModel:          roomModel,
		UserModel:          userModel,
		BBBApiWrapperModel: bbbApiWrapperModel,
		RecordingModel:     recordingModel,
		NatsService:        natsService,
	}
}

// HandleVerifyApiRequest is a middleware to verify BBB API requests.
func (bc *BBBController) HandleVerifyApiRequest(c *fiber.Ctx) error {
	apiKey := c.Params("apiKey")
	if apiKey == "" || apiKey != bc.AppConfig.Client.ApiKey {
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

	ourSum := bbbapiwrapper.CalculateCheckSum(bc.AppConfig.Client.Secret, method, queries)
	if subtle.ConstantTimeCompare([]byte(checksum), []byte(ourSum)) != 1 {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "checksumError", "Checksums do not match"))
	}

	return c.Next()
}

// HandleBBBCreate handles BBB create meeting requests.
func (bc *BBBController) HandleBBBCreate(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.CreateMeetingReq)
	var err error
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

	if err = validateRequest(pnmReq); err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", err.Error()))
	}

	room, err := bc.RoomModel.CreateRoom(pnmReq)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	return c.XML(bbbapiwrapper.CreateMeetingResp{
		ReturnCode:        "SUCCESS",
		MessageKey:        "success",
		Message:           "success",
		MeetingID:         q.MeetingID,
		InternalMeetingID: room.Sid,
		ParentMeetingID:   "bbb-none",
		AttendeePW:        q.AttendeePW,
		ModeratorPW:       q.ModeratorPW,
		CreateTime:        room.CreationTime * 1000,
		CreateDate:        time.Unix(room.CreationTime, 0).Format(time.RFC1123),
		VoiceBridge:       q.VoiceBridge,
		DialNumber:        q.DialNumber,
	})
}

// HandleBBBJoin handles BBB join meeting requests.
func (bc *BBBController) HandleBBBJoin(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.JoinMeetingReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	roomId := bbbapiwrapper.CheckMeetingIdToMatchFormat(q.MeetingID)
	metadata, err := bc.NatsService.GetRoomMetadataStruct(roomId)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	if metadata == nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "meeting is not active"))
	}

	ex := new(bbbapiwrapper.CreateMeetingDefaultExtraData)
	customDesign := new(plugnmeet.CustomDesignParams)
	if metadata.ExtraData != nil {
		err = json.Unmarshal([]byte(*metadata.ExtraData), ex)
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
		if metadata.ExtraData == nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", "did not found extra data"))
		}
		if subtle.ConstantTimeCompare([]byte(q.Password), []byte(ex.ModeratorPW)) == 1 {
			isAdmin = true
		}
	}

	req := bbbapiwrapper.ConvertJoinRequest(q, isAdmin)
	if err = validateRequest(req); err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", err.Error()))
	}

	exist := bc.NatsService.IsUserExistInBlockList(req.RoomId, req.UserInfo.UserId)
	if exist {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "validationError", "this user is blocked to join this session"))
	}

	token, err := bc.UserModel.GetPNMJoinToken(c.UserContext(), req)
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
	}

	ul := fmt.Sprintf("%s://%s/?access_token=%s", c.Protocol(), c.Hostname(), token)
	if bc.AppConfig.Client.BBBJoinHost != nil && *bc.AppConfig.Client.BBBJoinHost != "" {
		// use host name from config
		ul = fmt.Sprintf("%s/?access_token=%s", *bc.AppConfig.Client.BBBJoinHost, token)
	}
	if customDesign != nil && customDesign.String() != "" {
		op := protojson.MarshalOptions{
			EmitUnpopulated: false,
			UseProtoNames:   true,
		}
		cd, err := op.Marshal(customDesign)
		if err != nil {
			return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", err.Error()))
		}
		ul = fmt.Sprintf("%s&custom_design=%s", ul, string(cd))
	}

	if strings.ToLower(q.Redirect) == "false" {
		return c.XML(bbbapiwrapper.JoinMeetingRes{
			ReturnCode:   "SUCCESS",
			MessageKey:   "success",
			Message:      "You have joined successfully",
			MeetingID:    q.MeetingID,
			SessionToken: token,
			Url:          ul,
		})
	}

	return c.Redirect(ul)
}

// HandleBBBIsMeetingRunning handles BBB isMeetingRunning requests.
func (bc *BBBController) HandleBBBIsMeetingRunning(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	res, _, _, _ := bc.RoomModel.IsRoomActive(c.UserContext(), &plugnmeet.IsRoomActiveReq{
		RoomId: q.MeetingID,
	})

	return c.XML(bbbapiwrapper.IsMeetingRunningRes{
		ReturnCode: "SUCCESS",
		Running:    res.GetIsActive(),
	})
}

// HandleBBBGetMeetingInfo handles BBB getMeetingInfo requests.
func (bc *BBBController) HandleBBBGetMeetingInfo(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	status, msg, res := bc.RoomModel.GetActiveRoomInfo(c.UserContext(), &plugnmeet.GetActiveRoomInfoReq{
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

// HandleBBBGetMeetings handles BBB getMeetings requests.
func (bc *BBBController) HandleBBBGetMeetings(c *fiber.Ctx) error {
	_, _, rooms := bc.RoomModel.GetActiveRoomsInfo()

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

// HandleBBBEndMeetings handles BBB endMeeting requests.
func (bc *BBBController) HandleBBBEndMeetings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.MeetingReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	status, msg := bc.RoomModel.EndRoom(c.UserContext(), &plugnmeet.RoomEndReq{
		RoomId: bbbapiwrapper.CheckMeetingIdToMatchFormat(q.MeetingID),
	})

	if !status {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "error", msg))
	}
	return c.XML(bbbapiwrapper.CommonResponseMsg("SUCCESS", "sentEndMeetingRequest", "A request to end the meeting was sent.  Please wait a few seconds, and then use the getMeetingInfo or isMeetingRunning API calls to verify that it was ended"))
}

// HandleBBBGetRecordings handles BBB getRecordings requests.
func (bc *BBBController) HandleBBBGetRecordings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.GetRecordingsReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	host := fmt.Sprintf("%s://%s", c.Protocol(), c.Hostname())
	recordings, pagination, err := bc.BBBApiWrapperModel.GetRecordings(host, q)
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

// HandleBBBDeleteRecordings handles BBB deleteRecordings requests.
func (bc *BBBController) HandleBBBDeleteRecordings(c *fiber.Ctx) error {
	q := new(bbbapiwrapper.DeleteRecordingsReq)
	var err error
	if c.Method() == "POST" && c.Get("Content-Type") == "application/x-www-form-urlencoded" {
		err = c.BodyParser(q)
	} else {
		err = c.QueryParser(q)
	}
	if err != nil {
		return c.XML(bbbapiwrapper.CommonResponseMsg("FAILED", "parsingError", "We can not parse request"))
	}

	err = bc.RecordingModel.DeleteRecording(&plugnmeet.DeleteRecordingReq{
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
func (bc *BBBController) HandleBBBPublishRecordings(c *fiber.Ctx) error {
	return c.XML(bbbapiwrapper.PublishRecordingsRes{
		ReturnCode: "SUCCESS",
		Published:  true,
	})
}

// HandleBBBUpdateRecordings TO-DO: in the future
func (bc *BBBController) HandleBBBUpdateRecordings(c *fiber.Ctx) error {
	return c.XML(bbbapiwrapper.UpdateRecordingsRes{
		ReturnCode: "SUCCESS",
		Updated:    true,
	})
}
