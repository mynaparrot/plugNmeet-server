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
	return auth.GenerateTokenForDownloadRecording(path, m.app.Client.ApiKey, m.app.Client.Secret, m.app.RecorderInfo.TokenValidity)
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

	if err = out.Validate(jwt.Expected{Issuer: m.app.Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		if errors.Is(err, jwt.ErrExpired) {
			return nil, fiber.StatusUnauthorized, errors.New("token expired")
		}
		return nil, fiber.StatusUnauthorized, err
	}

	inputPath := out.Subject
	if inputPath == "" {
		return nil, fiber.StatusBadRequest, errors.New("invalid file path")
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
			return nil, fiber.StatusNotFound, config.ErrFileNotFound
		}
		return nil, fiber.StatusBadRequest, err
	}
	return &hooks.DownloadHookData{
		Action:     hooks.DownloadHookDataActionServeLocal,
		OutputPath: absolutePath,
		MimeType:   mType.String(),
	}, fiber.StatusOK, nil
}
