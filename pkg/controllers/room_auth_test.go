package controllers

import (
	"bytes"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var joinToken string

func setupApp() *fiber.App {
	app := fiber.New()
	room := app.Group("/room")
	room.Post("/create", HandleRoomCreate)
	room.Post("/getJoinToken", HandleGenerateJoinToken)
	room.Post("/isRoomActive", HandleIsRoomActive)
	room.Post("/getActiveRoomInfo", HandleGetActiveRoomInfo)
	room.Post("/getActiveRoomsInfo", HandleGetActiveRoomsInfo)
	room.Post("/endRoom", HandleEndRoom)

	// some APIs
	api := app.Group("/api", HandleVerifyHeaderToken)
	api.Post("/verifyToken", HandleVerifyToken)

	// others
	app.Post("/webhook", HandleWebhook)
	return app
}

func TestHandleRoomCreate(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.CreateRoomReq{
		RoomId: roomId,
		Metadata: &plugnmeet.RoomMetadata{
			RoomTitle:    "Test Room",
			RoomFeatures: &plugnmeet.RoomCreateFeatures{},
		},
	}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/create", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.CreateRoomRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)

	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "success", respBody.Msg)
	assert.NotNil(t, respBody.RoomInfo)
	assert.Equal(t, roomId, respBody.RoomInfo.RoomId)
}

func TestHandleGetJoinToken(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.GenerateTokenReq{
		RoomId: roomId,
		UserInfo: &plugnmeet.UserInfo{
			UserId: userId,
			Name:   "Test User",
			UserMetadata: &plugnmeet.UserMetadata{
				IsAdmin: true,
			},
		},
	}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/getJoinToken", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.GenerateTokenRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)

	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "success", respBody.Msg)
	assert.NotNil(t, respBody.Token)
	joinToken = strings.Clone(*respBody.Token)
}

func TestHandleValidateJoinToken(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.VerifyTokenReq{}

	bodyBytes, err := proto.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/verifyToken", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", joinToken)

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	// Read and unmarshal response
	respBody := new(plugnmeet.VerifyTokenRes)
	err = proto.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)

	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "token is valid", respBody.Msg)
	assert.NotNil(t, respBody.NatsSubjects)

	// now we can run some other test
	nts := NewNatsController()
	go nts.BootUp()

	// wait until finish bootup
	time.Sleep(time.Second * 1)
	testNatsJoin(t, joinToken, respBody.NatsSubjects)
}

func TestHandleIsRoomActive(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.IsRoomActiveReq{
		RoomId: roomId,
	}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/isRoomActive", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.IsRoomActiveRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)
	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "room is active", respBody.Msg)
	assert.True(t, respBody.IsActive)
}

func TestHandleGetActiveRoomInfo(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.GetActiveRoomInfoReq{
		RoomId: roomId,
	}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/getActiveRoomInfo", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.GetActiveRoomInfoRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)
	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "success", respBody.Msg)
	assert.NotNil(t, respBody.Room)
	assert.NotNil(t, respBody.Room.RoomInfo)
	assert.Equal(t, roomId, respBody.Room.RoomInfo.RoomId)
}

func TestHandleGetActiveRoomsInfo(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.GetActiveRoomsInfoRes{}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/getActiveRoomsInfo", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.GetActiveRoomsInfoRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)
	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "success", respBody.Msg)
	assert.GreaterOrEqual(t, len(respBody.Rooms), 1)
}

func TestHandleEndRoom(t *testing.T) {
	app := setupApp()
	reqBody := &plugnmeet.RoomEndReq{
		RoomId: roomId,
	}

	bodyBytes, err := protojson.Marshal(reqBody)
	assert.NoError(t, err)
	req := httptest.NewRequest("POST", "/room/endRoom", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")

	// Send request
	resp, err := app.Test(req)
	assert.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)

	// Read and unmarshal response
	respBody := new(plugnmeet.RoomEndRes)
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(resp.Body)
	assert.NoError(t, err)

	err = protojson.Unmarshal(buf.Bytes(), respBody)
	assert.NoError(t, err)
	// Compare expected values
	assert.True(t, respBody.Status)
	assert.Equal(t, "success", respBody.Msg)
}
