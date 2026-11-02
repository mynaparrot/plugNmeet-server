package controllers

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/bbbapiwrapper"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
)

func newBBBChecksumApp(apiKey, secret string) *fiber.App {
	bc := &BBBController{AppConfig: &config.AppConfig{Client: config.ClientInfo{ApiKey: apiKey, Secret: secret}}}

	app := fiber.New()
	app.Get("/bbb/api/:apiKey/create", bc.HandleVerifyApiRequest, func(c fiber.Ctx) error {
		return c.SendString("OK")
	})
	return app
}

func TestBBBVerifyApiRequest(t *testing.T) {
	const (
		apiKey = "test-api-key"
		secret = "test-secret"
	)
	app := newBBBChecksumApp(apiKey, secret)

	query := "meetingID=abc"
	checksum := bbbapiwrapper.CalculateCheckSum(secret, "create", query)

	tests := []struct {
		name     string
		url      string
		contains string
	}{
		{name: "valid", url: "/bbb/api/" + apiKey + "/create?" + query + "&checksum=" + checksum, contains: "OK"},
		{name: "wrong api key", url: "/bbb/api/wrong-key/create?" + query + "&checksum=" + checksum, contains: "apiKeyError"},
		{name: "wrong checksum", url: "/bbb/api/" + apiKey + "/create?" + query + "&checksum=deadbeef", contains: "checksumError"},
		{name: "missing checksum", url: "/bbb/api/" + apiKey + "/create?" + query, contains: "checksumError"},
		{name: "trailing parameter", url: "/bbb/api/" + apiKey + "/create?" + query + "&checksum=" + checksum + "&extra=1", contains: "checksumError"},
		{name: "multiple checksum", url: "/bbb/api/" + apiKey + "/create?" + query + "&checksum=" + checksum + "&checksum=" + checksum, contains: "checksumError"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest("GET", tt.url, nil))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}
			if !strings.Contains(string(body), tt.contains) {
				t.Fatalf("response %q does not contain %q", string(body), tt.contains)
			}
		})
	}
}
