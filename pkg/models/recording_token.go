package models

import (
	"errors"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
)

// recordingDownloadJWTIssuerSuffix separates recording download tokens from room
// access, LTI, and artifact download tokens.
const recordingDownloadJWTIssuerSuffix = "-recording"

func (m *RecordingModel) downloadJWTIssuer() string {
	return m.app.Client.ApiKey + recordingDownloadJWTIssuerSuffix
}

// GetDownloadToken will use the same JWT token generator as plugNmeet is using
func (m *RecordingModel) GetDownloadToken(r *plugnmeet.GetDownloadTokenReq) (string, error) {
	recording, err := m.FetchRecording(r.RecordId)
	if err != nil {
		return "", err
	}

	return m.CreateTokenForDownload(recording.FilePath)
}

// CreateTokenForDownload will generate token
// path format: sub_path/roomSid/filename
func (m *RecordingModel) CreateTokenForDownload(path string) (string, error) {
	return auth.GenerateTokenForDownloadRecording(path, m.downloadJWTIssuer(), m.app.Client.Secret, m.app.RecorderInfo.TokenValidity)
}

// VerifyRecordingToken verify token & provide file path
func (m *RecordingModel) VerifyRecordingToken(token string) (*hooks.DownloadHookData, int, error) {
	log := m.logger.WithField("method", "VerifyRecordingToken")

	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return nil, fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(m.app.Client.Secret), &out); err != nil {
		return nil, fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{Issuer: m.downloadJWTIssuer(), Time: time.Now().UTC()}); err != nil {
		if errors.Is(err, jwt.ErrExpired) {
			return nil, fiber.StatusUnauthorized, errors.New("token expired")
		}
		return nil, fiber.StatusUnauthorized, err
	}

	// validate before handing the path to hooks, which may act on it
	inputPath, err := sanitizeRelativeFilePath(out.Subject)
	if err != nil {
		return nil, fiber.StatusBadRequest, err
	}

	if m.app.Hooks != nil {
		// Hooks are defined, so use the pipeline.
		req := hooks.DownloadHookData{
			InputPath:    inputPath,
			HookFileType: hooks.HookFileTypeRecording,
		}
		res, err := m.app.Hooks.RunDownloadHook(m.ctx, &req, nil, 0, log)
		if err != nil {
			log.WithError(err).Error("download hook pipeline failed")
			return nil, fiber.StatusInternalServerError, errors.New("download hook pipeline failed")
		}

		if res != nil && (res.OutputPath != "" || res.RedirectUrl != "") {
			return res, fiber.StatusOK, nil
		}
	}

	// If no hooks are defined or no output from script, fallback to local.
	absolutePath, mType, err := helpers.ValidateAndGetAbsFilePath(m.app.RecorderInfo.RecordingFilesPath, inputPath)
	if err != nil {
		if errors.Is(err, config.ErrFileNotFound) {
			// the sanitized relative path lets the caller name the file, while the
			// resolved path and the error text stay server-side
			return &hooks.DownloadHookData{InputPath: inputPath}, fiber.StatusNotFound, config.ErrFileNotFound
		}
		return nil, fiber.StatusBadRequest, err
	}
	return &hooks.DownloadHookData{
		Action:     hooks.DownloadHookDataActionServeLocal,
		OutputPath: absolutePath,
		MimeType:   mType.String(),
	}, fiber.StatusOK, nil
}
