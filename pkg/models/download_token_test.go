package models

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

type downloadTestEnv struct {
	rm  *RecordingModel
	am  *ArtifactModel
	lti *LtiV1Model
}

func newDownloadTestEnv(t *testing.T) *downloadTestEnv {
	t.Helper()

	validity := time.Minute
	storagePath := t.TempDir()
	app := &config.AppConfig{
		Client: config.ClientInfo{ApiKey: testApiKey, Secret: testSecret},
		RecorderInfo: config.RecorderInfo{
			RecordingFilesPath: storagePath,
			TokenValidity:      validity,
		},
		ArtifactsSettings: &config.ArtifactsSettings{
			StoragePath:   &storagePath,
			TokenValidity: &validity,
		},
	}

	// real files so both verifiers can resolve them successfully
	for _, name := range []string{"room-sid/recording.mp4", "room-sid/transcript.vtt"} {
		p := filepath.Join(storagePath, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("failed to create dir: %v", err)
		}
		if err := os.WriteFile(p, []byte("test"), 0o600); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}
	}

	log := logrus.NewEntry(logrus.New())
	return &downloadTestEnv{
		rm:  &RecordingModel{ctx: context.Background(), app: app, logger: log},
		am:  &ArtifactModel{ctx: context.Background(), app: app, log: log},
		lti: &LtiV1Model{app: app},
	}
}

func TestDownloadTokensAreNotInterchangeable(t *testing.T) {
	env := newDownloadTestEnv(t)

	recordingToken, err := env.rm.CreateTokenForDownload("room-sid/recording.mp4")
	if err != nil {
		t.Fatalf("failed to create recording token: %v", err)
	}
	artifactToken, err := env.am.generateToken("room-sid/transcript.vtt")
	if err != nil {
		t.Fatalf("failed to create artifact token: %v", err)
	}
	roomToken, err := auth.GeneratePlugNmeetJWTAccessToken(testApiKey, testSecret, "user-id", time.Minute, &plugnmeet.PlugNmeetTokenClaims{RoomId: "room-id"})
	if err != nil {
		t.Fatalf("failed to create room access token: %v", err)
	}
	ltiToken, err := env.lti.ToJWT(&plugnmeet.LtiClaims{UserId: "user-id", RoomId: "room-id"})
	if err != nil {
		t.Fatalf("failed to create lti token: %v", err)
	}

	verifiers := map[string]func(token string) error{
		"recording": func(token string) error {
			_, _, err := env.rm.VerifyRecordingToken(token)
			return err
		},
		"artifact": func(token string) error {
			_, _, err := env.am.VerifyArtifactDownloadJWT(token)
			return err
		},
		"room-access": func(token string) error {
			_, err := auth.VerifyPlugNmeetAccessToken(testApiKey, testSecret, token, 0)
			return err
		},
		"lti": func(token string) error {
			_, err := env.lti.LTIV1VerifyHeaderToken(token)
			return err
		},
	}

	tokens := map[string]string{
		"recording":   recordingToken,
		"artifact":    artifactToken,
		"room-access": roomToken,
		"lti":         ltiToken,
	}

	for tokenName, token := range tokens {
		for verifierName, verify := range verifiers {
			err := verify(token)
			if tokenName == verifierName && err != nil {
				t.Errorf("%s token rejected by its own verifier: %v", tokenName, err)
			}
			if tokenName != verifierName && err == nil {
				t.Errorf("%s token accepted by %s verifier", tokenName, verifierName)
			}
		}
	}
}

func TestDownloadTokensRejectUnsafePaths(t *testing.T) {
	env := newDownloadTestEnv(t)

	// a configured download hook makes sure the path is validated before hooks run
	env.rm.app.Hooks = &hooks.Config{
		DownloadHook: &hooks.HookScriptConfig{
			Scripts: []hooks.HookScript{{Script: "/bin/true"}},
		},
	}

	unsafePaths := []string{"", "../../etc/passwd", "/etc/passwd", "room-sid/../../etc/passwd"}

	for _, path := range unsafePaths {
		t.Run("recording "+path, func(t *testing.T) {
			token, err := auth.GenerateTokenForDownloadRecording(path, env.rm.downloadJWTIssuer(), testSecret, time.Minute)
			if err != nil {
				t.Fatalf("failed to sign token: %v", err)
			}

			res, status, err := env.rm.VerifyRecordingToken(token)
			if err == nil || status != fiber.StatusBadRequest || res != nil {
				t.Fatalf("expected bad request, got status=%d res=%v err=%v", status, res, err)
			}
		})

		t.Run("artifact "+path, func(t *testing.T) {
			token, err := env.am.generateToken(path)
			if err != nil {
				t.Fatalf("failed to sign token: %v", err)
			}

			res, status, err := env.am.VerifyArtifactDownloadJWT(token)
			if err == nil || status != fiber.StatusBadRequest || res != nil {
				t.Fatalf("expected bad request, got status=%d res=%v err=%v", status, res, err)
			}
		})
	}
}
