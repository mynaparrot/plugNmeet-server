package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/auth"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
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
func (m *RecordingModel) VerifyRecordingToken(token string) (string, *mimetype.MIME, int, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return "", nil, fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(m.app.Client.Secret), &out); err != nil {
		return "", nil, fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{Issuer: m.app.Client.ApiKey, Time: time.Now().UTC()}); err != nil {
		if errors.Is(err, jwt.ErrExpired) {
			return "", nil, fiber.StatusUnauthorized, errors.New("token expired")
		}
		return "", nil, fiber.StatusUnauthorized, err
	}

	file := fmt.Sprintf("%s/%s", m.app.RecorderInfo.RecordingFilesPath, out.Subject)
	mType, err := mimetype.DetectFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, fiber.StatusNotFound, fmt.Errorf("record file %s not found", filepath.Base(file))
		}
		return "", nil, fiber.StatusInternalServerError, err
	}

	return file, mType, fiber.StatusOK, nil
}
