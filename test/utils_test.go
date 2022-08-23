package test

import (
	"bytes"
	"github.com/goccy/go-json"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/handler"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// utils
func prepareStringReq(method, router, b string) *http.Request {
	req := httptest.NewRequest(method, router, strings.NewReader(b))
	req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
	req.Header.Set("API-SECRET", config.AppCnf.Client.Secret)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func prepareByteReq(method, router string, b []byte) *http.Request {
	req := httptest.NewRequest(method, router, bytes.NewReader(b))
	req.Header.Set("API-KEY", config.AppCnf.Client.ApiKey)
	req.Header.Set("API-SECRET", config.AppCnf.Client.Secret)
	req.Header.Set("Content-Type", "application/protobuf")
	return req
}

func performCommonReq(t *testing.T, req *http.Request, expectedStatus bool) {
	router := handler.Router()

	res, err := router.Test(req)
	if err != nil {
		t.Error(err)
	}

	if res.StatusCode != 200 {
		t.Errorf("Error code: %d", res.StatusCode)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
	}

	rr := new(plugnmeet.CommonResponse)
	err = json.Unmarshal(body, rr)
	if err != nil {
		t.Error(err)
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
	}
	if res.StatusCode != 200 {
		t.Errorf("Error in router: %s, Error code: %d", "/auth/room/create", res.StatusCode)
	}
}
