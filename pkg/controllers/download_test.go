package controllers

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/models"
	"github.com/sirupsen/logrus"
)

const (
	downloadTestApiKey = "download-test-api-key"
	// HS256 needs a key of at least 32 bytes
	downloadTestSecret = "download-test-secret-at-least-32-bytes"
	// artifactIssuerSuffix mirrors the unexported models.artifactDownloadJWTIssuerSuffix.
	artifactIssuerSuffix = "-artifact"
)

type downloadTestEnv struct {
	recording   *models.RecordingModel
	storagePath string
	recordingID fiber.Handler
	artifactID  fiber.Handler
}

func newDownloadTestEnv(t *testing.T) *downloadTestEnv {
	t.Helper()

	validity := time.Minute
	storagePath := t.TempDir()
	app := &config.AppConfig{
		Client: config.ClientInfo{ApiKey: downloadTestApiKey, Secret: downloadTestSecret},
		RecorderInfo: config.RecorderInfo{
			RecordingFilesPath: storagePath,
			TokenValidity:      validity,
		},
		ArtifactsSettings: &config.ArtifactsSettings{
			StoragePath:   &storagePath,
			TokenValidity: &validity,
		},
	}

	logger := logrus.New()
	rm := models.NewRecordingModel(models.RecordingModelArgs{
		Ctx:    context.Background(),
		App:    app,
		Logger: logger,
	})
	am := models.NewArtifactModel(models.ArtifactModelArgs{
		Ctx:    context.Background(),
		App:    app,
		Logger: logger,
	})

	rc := NewRecordingController(RecordingControllerArgs{RecordingModel: rm, Logger: logger})
	ac := NewArtifactController(ArtifactControllerArgs{ArtifactModel: am, Logger: logger})

	return &downloadTestEnv{
		recording:   rm,
		storagePath: storagePath,
		recordingID: rc.HandleDownloadRecording,
		artifactID:  ac.HandleDownloadArtifact,
	}
}

// writeFile creates a file inside the download storage and returns its relative path.
func (e *downloadTestEnv) writeFile(t *testing.T, relPath string) string {
	t.Helper()

	p := filepath.Join(e.storagePath, relPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(p, []byte("file-content"), 0o600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	return p
}

// artifactToken signs a download token the way the artifact model does.
func (e *downloadTestEnv) artifactToken(t *testing.T, subject string) string {
	t.Helper()

	token, err := auth.GenerateTokenForDownloadRecording(subject, downloadTestApiKey+artifactIssuerSuffix, downloadTestSecret, time.Minute)
	if err != nil {
		t.Fatalf("failed to sign artifact token: %v", err)
	}
	return token
}

func (e *downloadTestEnv) recordingToken(t *testing.T, subject string) string {
	t.Helper()

	token, err := e.recording.CreateTokenForDownload(subject)
	if err != nil {
		t.Fatalf("failed to sign recording token: %v", err)
	}
	return token
}

type downloadHandlerCase struct {
	name    string
	handler fiber.Handler
}

func downloadHandlerCases(env *downloadTestEnv) []downloadHandlerCase {
	return []downloadHandlerCase{
		{name: "recording", handler: env.recordingID},
		{name: "artifact", handler: env.artifactID},
	}
}

// sendDownloadRequest calls the download route and returns status and body text.
// An empty token exercises the route without the token parameter.
func sendDownloadRequest(t *testing.T, handler fiber.Handler, token string) (int, string) {
	t.Helper()

	app := fiber.New()
	app.Get("/download", handler)
	app.Get("/download/:token", handler)

	path := "/download"
	if token != "" {
		path = "/download/" + token
	}

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func assertNoLeak(t *testing.T, body string, leaks ...string) {
	t.Helper()

	for _, leak := range leaks {
		if leak != "" && strings.Contains(body, leak) {
			t.Errorf("response body %q leaks %q", body, leak)
		}
	}
}

func TestDownloadMissingToken(t *testing.T) {
	env := newDownloadTestEnv(t)

	for _, c := range downloadHandlerCases(env) {
		t.Run(c.name, func(t *testing.T) {
			status, body := sendDownloadRequest(t, c.handler, "")
			if status != fiber.StatusUnauthorized {
				t.Errorf("status = %d, want %d", status, fiber.StatusUnauthorized)
			}
			if body != config.MsgFileDownloadTokenInvalid {
				t.Errorf("body = %q, want %q", body, config.MsgFileDownloadTokenInvalid)
			}
			assertNoLeak(t, body, env.storagePath, downloadTestSecret, "jwt")
		})
	}
}

func TestDownloadInvalidToken(t *testing.T) {
	env := newDownloadTestEnv(t)

	for _, c := range downloadHandlerCases(env) {
		t.Run(c.name, func(t *testing.T) {
			status, body := sendDownloadRequest(t, c.handler, "not-a-token")
			if status != fiber.StatusUnauthorized {
				t.Errorf("status = %d, want %d", status, fiber.StatusUnauthorized)
			}
			if body != config.MsgFileDownloadTokenInvalid {
				t.Errorf("body = %q, want %q", body, config.MsgFileDownloadTokenInvalid)
			}
			// neither the raw token nor the verifier error text may be echoed
			assertNoLeak(t, body, "not-a-token", env.storagePath, downloadTestSecret, "jwt")
		})
	}
}

func TestDownloadVerifierNotFound(t *testing.T) {
	env := newDownloadTestEnv(t)

	cases := []struct {
		downloadHandlerCase
		token func(t *testing.T) string
	}{
		{downloadHandlerCase{"recording", env.recordingID}, func(t *testing.T) string { return env.recordingToken(t, "room-sid/missing.mp4") }},
		{downloadHandlerCase{"artifact", env.artifactID}, func(t *testing.T) string { return env.artifactToken(t, "room-sid/missing.mp4") }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body := sendDownloadRequest(t, c.handler, c.token(t))
			if status != fiber.StatusNotFound {
				t.Errorf("status = %d, want %d", status, fiber.StatusNotFound)
			}
			// the verified file may be named by its basename only
			want := config.FileDownloadNotFoundMsg("room-sid/missing.mp4")
			if body != want {
				t.Errorf("body = %q, want %q", body, want)
			}
			if !strings.Contains(body, "missing.mp4") {
				t.Errorf("body = %q, want it to name the file", body)
			}
			assertNoLeak(t, body, env.storagePath, "room-sid", "file not found")
		})
	}
}

// TestDownloadNotFoundWithoutPath keeps the generic not-found text when the
// verifier has no trustworthy file name to offer.
func TestDownloadNotFoundWithoutPath(t *testing.T) {
	logger := logrus.New()
	handler := func(c fiber.Ctx) error {
		return downloadTokenErrorResponse(c, nil, fiber.StatusNotFound, config.ErrFileNotFound, logger.WithField("test", true))
	}

	status, body := sendDownloadRequest(t, handler, "token")
	if status != fiber.StatusNotFound {
		t.Errorf("status = %d, want %d", status, fiber.StatusNotFound)
	}
	if body != config.MsgFileDownloadNotFound {
		t.Errorf("body = %q, want %q", body, config.MsgFileDownloadNotFound)
	}
}

func TestDownloadVerifierInvalidPath(t *testing.T) {
	env := newDownloadTestEnv(t)

	for _, c := range downloadHandlerCases(env) {
		t.Run(c.name, func(t *testing.T) {
			token := env.recordingToken(t, "../../etc/passwd")
			if c.name == "artifact" {
				token = env.artifactToken(t, "../../etc/passwd")
			}

			status, body := sendDownloadRequest(t, c.handler, token)
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d, want %d", status, fiber.StatusBadRequest)
			}
			if body != config.MsgFileDownloadInvalidPath {
				t.Errorf("body = %q, want %q", body, config.MsgFileDownloadInvalidPath)
			}
			assertNoLeak(t, body, env.storagePath, "etc/passwd", "passwd", "invalid file path")
		})
	}
}

func TestDownloadServesFile(t *testing.T) {
	env := newDownloadTestEnv(t)
	env.writeFile(t, "room-sid/recording.mp4")

	status, body := sendDownloadRequest(t, env.recordingID, env.recordingToken(t, "room-sid/recording.mp4"))
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want %d", status, fiber.StatusOK)
	}
	if body != "file-content" {
		t.Errorf("body = %q, want the file content", body)
	}
}

// TestDownloadServeFileFailure covers the failures between verification and the
// response, which must never expose the resolved path or a raw error.
func TestDownloadServeFileFailure(t *testing.T) {
	original := sendFile
	t.Cleanup(func() { sendFile = original })

	env := newDownloadTestEnv(t)
	absPath := env.writeFile(t, "room-sid/recording.mp4")
	basename := filepath.Base(absPath)

	t.Run("file disappeared", func(t *testing.T) {
		sendFile = func(c fiber.Ctx, path string) error {
			return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}

		status, body := sendDownloadRequest(t, env.recordingID, env.recordingToken(t, "room-sid/recording.mp4"))
		if status != fiber.StatusNotFound {
			t.Errorf("status = %d, want %d", status, fiber.StatusNotFound)
		}
		if body != config.FileDownloadNotFoundMsg(absPath) {
			t.Errorf("body = %q, want %q", body, config.FileDownloadNotFoundMsg(absPath))
		}
		// only the basename may be shown, never the directory
		if !strings.Contains(body, basename) {
			t.Errorf("body = %q, want it to name %q", body, basename)
		}
		assertNoLeak(t, body, env.storagePath, "room-sid", "open")
	})

	t.Run("unexpected serving error", func(t *testing.T) {
		sendFile = func(c fiber.Ctx, path string) error {
			return errors.New("read failure on " + path)
		}

		status, body := sendDownloadRequest(t, env.recordingID, env.recordingToken(t, "room-sid/recording.mp4"))
		if status != fiber.StatusInternalServerError {
			t.Errorf("status = %d, want %d", status, fiber.StatusInternalServerError)
		}
		if body != config.MsgFileDownloadFailed {
			t.Errorf("body = %q, want %q", body, config.MsgFileDownloadFailed)
		}
		assertNoLeak(t, body, env.storagePath, basename, "read failure")
	})
}

func TestFileDownloadNotFoundMsg(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/srv/recordings/room-sid/recording.mp4", "File not found: recording.mp4."},
		{"", config.MsgFileDownloadNotFound},
		{"/", config.MsgFileDownloadNotFound},
	}

	for _, c := range cases {
		if got := config.FileDownloadNotFoundMsg(c.path); got != c.want {
			t.Errorf("FileDownloadNotFoundMsg(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}
