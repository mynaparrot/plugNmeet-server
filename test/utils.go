package test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/goccy/go-json"
	lksdk "github.com/livekit/server-sdk-go"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/handler"
	"google.golang.org/protobuf/proto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func prepareStringReq(method, router, body string) *http.Request {
	mac := hmac.New(sha256.New, []byte(config.AppCnf.Client.Secret))
	mac.Write([]byte(body))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(method, router, strings.NewReader(body))
	req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
	req.Header.Set("HASH-SIGNATURE", signature)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func prepareStringWithTokenReq(token, method, router string, m proto.Message) *http.Request {
	b, _ := proto.Marshal(m)
	req := httptest.NewRequest(method, router, bytes.NewReader(b))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func prepareByteReq(method, router string, b []byte) *http.Request {
	mac := hmac.New(sha256.New, []byte(config.AppCnf.Client.Secret))
	mac.Write(b)
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(method, router, bytes.NewReader(b))
	req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
	req.Header.Set("HASH-SIGNATURE", signature)
	req.Header.Set("Content-Type", "application/protobuf")
	return req
}

func performCommonReq(t *testing.T, req *http.Request, expectedStatus bool) {
	router := handler.Router()

	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
		return
	}

	if res.StatusCode != 200 {
		t.Errorf("Error code: %d", res.StatusCode)
		return
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
		return
	}

	rr := new(plugnmeet.CommonResponse)
	err = json.Unmarshal(body, rr)
	if err != nil {
		t.Error(err)
		return
	}

	if rr.Status != expectedStatus {
		t.Errorf("Expected: %t, Got: %t, Msg: %s", expectedStatus, rr.Status, rr.Msg)
	}
}

func performCommonProtoReq(t *testing.T, req *http.Request, expectedStatus bool) {
	router := handler.Router()

	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
		return
	}

	if res.StatusCode != 200 {
		t.Errorf("Error code: %d", res.StatusCode)
		return
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
		return
	}

	rr := new(plugnmeet.CommonResponse)
	err = proto.Unmarshal(body, rr)
	if err != nil {
		t.Error(err)
		return
	}

	if rr.Status != expectedStatus {
		t.Errorf("Expected: %t, Got: %t, Msg: %s", expectedStatus, rr.Status, rr.Msg)
	}
}

func performCommonStatusReq(t *testing.T, req *http.Request) {
	router := handler.Router()
	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
		return
	}
	if res.StatusCode != 200 {
		t.Errorf("Error in router: %s, Error code: %d", "/auth/room/create", res.StatusCode)
	}
}

func connectLivekit(t *testing.T, token, livekitUrl string) *lksdk.Room {
	room := new(lksdk.Room)

	t.Run("connectLivekit", func(t *testing.T) {
		roomCB := &lksdk.RoomCallback{
			OnRoomMetadataChanged: func(m string) {
				fmt.Println("OnRoomMetadataChanged")
			},
			OnParticipantConnected: func(p *lksdk.RemoteParticipant) {
				fmt.Printf("%s Joined \n", p.Name())
			},
			OnDisconnected: func() {
				fmt.Println("Room disconnected")
			},
		}

		r, err := lksdk.ConnectToRoomWithToken(livekitUrl, token, roomCB)
		if err != nil {
			t.Errorf("Can't connect to room. Error: %s", err.Error())
		}

		room = r
	})

	return room
}
