package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
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
func (m *RecordingModel) VerifyRecordingToken(token string) (*config.DownloadHookResponse, int, error) {
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

	logicalPath := out.Subject
	if logicalPath == "" {
		return nil, fiber.StatusBadRequest, errors.New("invalid file path")
	}

	// If no hooks are defined, fallback to local.
	if m.app.StorageHooks == nil || len(m.app.StorageHooks.DownloadHook) == 0 {
		absolutePath, mType, err := helpers.ValidateAndGetAbsFilePath(m.app.RecorderInfo.RecordingFilesPath, logicalPath)
		if err != nil {
			if errors.Is(err, config.ErrFileNotFound) {
				return nil, fiber.StatusNotFound, config.ErrFileNotFound
			}
			return nil, fiber.StatusBadRequest, err
		}
		return &config.DownloadHookResponse{
			Action:    "serve_local",
			LocalPath: absolutePath,
			MimeType:  mType.String(),
		}, fiber.StatusOK, nil
	}

	// Hooks are defined, so use the pipeline.
	req := config.DownloadHookRequest{
		LogicalPath: logicalPath,
		ServiceType: "recording",
	}

	resBytes, err := config.ExecuteHookPipeline(m.ctx, m.app.StorageHooks.DownloadHook, &req, m.logger)
	if err != nil {
		m.logger.WithError(err).Error("download hook pipeline failed")
		return nil, fiber.StatusInternalServerError, errors.New("download hook pipeline failed")
	}

	var res config.DownloadHookResponse
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fiber.StatusInternalServerError, fmt.Errorf("failed to unmarshal download hook response: %w", err)
	}

	if res.Error != "" {
		return nil, fiber.StatusInternalServerError, fmt.Errorf("download hook script returned an error: %s", res.Error)
	}

	return &res, fiber.StatusOK, nil
}
