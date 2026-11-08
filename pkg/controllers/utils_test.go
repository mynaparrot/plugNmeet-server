package controllers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
)

// newCallerTestApp mounts handler behind the same trusted classification that the
// integrator route groups (/auth, /lti, /bbb) install in the router.
func newCallerTestApp(handler fiber.Handler, asIntegrator bool) *fiber.App {
	app := fiber.New()
	app.Post("/test", func(c fiber.Ctx) error {
		if asIntegrator {
			c.Locals(config.LocalsCallerKey, config.CallerIntegrator)
		}
		return c.Next()
	}, handler)
	return app
}

func postCallerTestApp(t *testing.T, handler fiber.Handler, asIntegrator bool, body string) string {
	t.Helper()
	app := newCallerTestApp(handler, asIntegrator)
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return string(b)
}

func TestCallerMsgFormatting(t *testing.T) {
	handler := func(c fiber.Ctx) error {
		// a fixed raw operational text, as a model error would produce it
		return c.SendString(callerMsg(c, "redis: connection refused"))
	}
	keyHandler := func(c fiber.Ctx) error {
		return c.SendString(callerMsg(c, config.ErrRoomNotFound.Error()))
	}
	successHandler := func(c fiber.Ctx) error {
		return c.SendString(callerMsg(c, config.MsgSuccess))
	}

	t.Run("client keeps the i18n key", func(t *testing.T) {
		got := postCallerTestApp(t, keyHandler, false, "")
		if got != config.ErrRoomNotFound.Error() {
			t.Fatalf("body = %q, want the client i18n key %q", got, config.ErrRoomNotFound.Error())
		}
	})

	t.Run("client success stays a key", func(t *testing.T) {
		if got := postCallerTestApp(t, successHandler, false, ""); got != config.MsgSuccess {
			t.Fatalf("body = %q, want %q", got, config.MsgSuccess)
		}
	})

	t.Run("integrator gets curated English", func(t *testing.T) {
		got := postCallerTestApp(t, keyHandler, true, "")
		if got != config.MsgAuthRoomNotFound {
			t.Fatalf("body = %q, want %q", got, config.MsgAuthRoomNotFound)
		}
		if strings.Contains(got, "room.errors.") {
			t.Fatalf("integrator response %q exposes a client i18n key", got)
		}
	})

	t.Run("integrator success is plain text", func(t *testing.T) {
		if got := postCallerTestApp(t, successHandler, true, ""); got != config.MsgIntegratorSuccess {
			t.Fatalf("body = %q, want %q", got, config.MsgIntegratorSuccess)
		}
	})

	t.Run("raw operational text is never forwarded", func(t *testing.T) {
		got := postCallerTestApp(t, handler, true, "")
		if got != config.MsgIntegratorUnexpected {
			t.Fatalf("body = %q, want %q", got, config.MsgIntegratorUnexpected)
		}
		if strings.Contains(got, "redis") {
			t.Fatalf("integrator response %q leaks internal detail", got)
		}
	})
}

// TestRoomCreateInvalidRequestClassification proves the same handler answers a
// client caller with an i18n key and an integrator with curated English. The
// Auth API handlers answer with a protojson body, hence the JSON decoding.
func TestRoomCreateInvalidRequestClassification(t *testing.T) {
	rc := &RoomController{}

	tests := []struct {
		name         string
		asIntegrator bool
		wantMsg      string
	}{
		{name: "client", asIntegrator: false, wantMsg: config.ErrRoomInvalidRequest.Error()},
		{name: "integrator", asIntegrator: true, wantMsg: config.MsgAuthInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newCallerTestApp(rc.HandleRoomCreate, tt.asIntegrator)
			resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("not-json")))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("http status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}
			res := new(plugnmeet.CommonResponse)
			if err = protojson.Unmarshal(body, res); err != nil {
				t.Fatalf("failed to decode CommonResponse: %v", err)
			}
			if res.GetMsg() != tt.wantMsg {
				t.Fatalf("msg = %q, want %q", res.GetMsg(), tt.wantMsg)
			}
			if strings.Contains(res.GetMsg(), "room.errors.") != !tt.asIntegrator {
				t.Fatalf("msg = %q does not match the caller class", res.GetMsg())
			}
		})
	}
}

// TestLtiMessagesAreCurated proves the LTI endpoints answer the LMS consumer with
// plain English and never with a client i18n key.
func TestLtiMessagesAreCurated(t *testing.T) {
	lc := &LtiV1Controller{}
	app := newCallerTestApp(lc.HandleLTIV1GETREQUEST, true)

	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/test", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if resp.StatusCode != fiber.StatusMethodNotAllowed {
		t.Fatalf("http status = %d, want %d", resp.StatusCode, fiber.StatusMethodNotAllowed)
	}
	if !strings.Contains(string(body), config.MsgLTIPostRequired) {
		t.Fatalf("body %q does not contain %q", string(body), config.MsgLTIPostRequired)
	}
}
