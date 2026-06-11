package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"google.golang.org/protobuf/encoding/protojson"
)

// FetchArtifacts fetches records from the DB and formats them for the API response.
func (m *ArtifactModel) FetchArtifacts(req *plugnmeet.FetchArtifactsReq) (*plugnmeet.FetchArtifactsResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	} else if req.Limit > 100 {
		req.Limit = 100
	}
	if req.OrderBy == "" {
		req.OrderBy = "DESC"
	}

	dbArtifacts, total, err := m.ds.GetArtifacts(req.RoomIds, req.RoomSid, req.Type, req.From, req.Limit, &req.OrderBy)
	if err != nil {
		return nil, err
	}
	if total == 0 {
		return nil, config.NotFoundErr
	}

	result := &plugnmeet.FetchArtifactsResult{
		TotalArtifacts: total,
		From:           req.From,
		Limit:          req.Limit,
		OrderBy:        req.OrderBy,
		Type:           req.Type,
	}

	for _, dbArtifact := range dbArtifacts {
		metadata := new(plugnmeet.RoomArtifactMetadata)
		if err := protojson.Unmarshal([]byte(dbArtifact.Metadata), metadata); err == nil {
			result.ArtifactsList = append(result.ArtifactsList, &plugnmeet.ArtifactInfo{
				ArtifactId: dbArtifact.ArtifactId,
				RoomId:     dbArtifact.RoomId,
				Type:       plugnmeet.RoomArtifactType(dbArtifact.Type),
				Created:    dbArtifact.Created.Format(time.RFC3339),
				Metadata:   metadata,
			})
		}
	}

	return result, nil
}

func (m *ArtifactModel) GetArtifactInfoByArtifactId(artifactId string) (*plugnmeet.ArtifactInfoRes, error) {
	dbArtifact, roomInfo, err := m.ds.GetRoomArtifactDetails(artifactId)
	if err != nil {
		return nil, err
	}

	metadata := new(plugnmeet.RoomArtifactMetadata)
	err = protojson.Unmarshal([]byte(dbArtifact.Metadata), metadata)
	if err != nil {
		return nil, err
	}

	res := &plugnmeet.ArtifactInfoRes{
		Status:     true,
		Msg:        "success",
		StatusCode: plugnmeet.StatusCode_SUCCESS,
		ArtifactInfo: &plugnmeet.ArtifactInfo{
			ArtifactId: dbArtifact.ArtifactId,
			RoomId:     dbArtifact.RoomId,
			Type:       plugnmeet.RoomArtifactType(dbArtifact.Type),
			Created:    dbArtifact.Created.Format(time.RFC3339),
			Metadata:   metadata,
		},
	}

	if roomInfo != nil {
		res.RoomInfo = &plugnmeet.PastRoomInfo{
			RoomTitle:          roomInfo.RoomTitle,
			RoomId:             roomInfo.RoomId,
			RoomSid:            roomInfo.Sid,
			JoinedParticipants: roomInfo.JoinedParticipants,
			WebhookUrl:         roomInfo.WebhookUrl,
			Created:            roomInfo.Created.Format("2006-01-02 15:04:05"),
			Ended:              roomInfo.Ended.Format("2006-01-02 15:04:05"),
		}
	}

	return res, nil
}

// generateToken now generates a JWT with a file path.
func (m *ArtifactModel) generateToken(filePath string) (string, error) {
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.HS256, Key: []byte(m.app.Client.Secret)}, (&jose.SignerOptions{}).WithType("JWT"))

	if err != nil {
		return "", err
	}

	cl := jwt.Claims{
		Issuer:    m.app.Client.ApiKey,
		NotBefore: jwt.NewNumericDate(time.Now().UTC()),
		Expiry:    jwt.NewNumericDate(time.Now().UTC().Add(*m.app.ArtifactsSettings.TokenValidity)),
		Subject:   filePath,
	}

	return jwt.Signed(sig).Claims(cl).Serialize()
}

// GetArtifactDownloadToken checks permissions and generates a JWT containing the file path.
func (m *ArtifactModel) GetArtifactDownloadToken(req *plugnmeet.GetArtifactDownloadTokenReq) (string, error) {
	artifact, err := m.ds.GetRoomArtifactByArtifactID(req.ArtifactId)
	if err != nil {
		return "", err
	}
	if artifact == nil {
		return "", config.NotFoundErr
	}

	if !m.isDownloadable(plugnmeet.RoomArtifactType(artifact.Type)) {
		return "", fmt.Errorf("'%s' artifact type is not downloadable", artifact.Type)
	}

	var metadata plugnmeet.RoomArtifactMetadata
	err = protojson.Unmarshal([]byte(artifact.Metadata), &metadata)
	if err != nil || metadata.FileInfo == nil || metadata.FileInfo.FilePath == "" {
		return "", errors.New("artifact has no downloadable file")
	}
	// Generate the token with the file path.
	return m.generateToken(metadata.FileInfo.FilePath)
}

// VerifyArtifactDownloadJWT validates a JWT and returns either a local file path or a redirect URL.
func (m *ArtifactModel) VerifyArtifactDownloadJWT(token string) (*hooks.DownloadHookData, int, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return nil, fiber.StatusUnauthorized, err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(m.app.Client.Secret), &out); err != nil {
		return nil, fiber.StatusUnauthorized, err
	}

	if err = out.Validate(jwt.Expected{
		Issuer: m.app.Client.ApiKey,
		Time:   time.Now().UTC(),
	}); err != nil {
		if errors.Is(err, jwt.ErrExpired) {
			return nil, fiber.StatusUnauthorized, errors.New("token expired")
		}
		return nil, fiber.StatusUnauthorized, err
	}

	logicalPath := out.Subject
	if logicalPath == "" {
		return nil, fiber.StatusBadRequest, errors.New("invalid file path in token")
	}

	// If no hooks are defined, fallback to local.
	if m.app.StorageHooks == nil || len(m.app.StorageHooks.DownloadHook) == 0 {
		absolutePath, mType, err := helpers.ValidateAndGetAbsFilePath(*m.app.ArtifactsSettings.StoragePath, logicalPath)
		if err != nil {
			if errors.Is(err, config.ErrFileNotFound) {
				return nil, fiber.StatusNotFound, config.ErrFileNotFound
			}
			return nil, fiber.StatusBadRequest, err
		}
		return &hooks.DownloadHookData{
			Action:     "serve_local",
			OutputPath: absolutePath,
			MimeType:   mType.String(),
		}, fiber.StatusOK, nil
	}

	// Hooks are defined, so use the pipeline.
	req := hooks.DownloadHookData{
		InputPath:   logicalPath,
		ServiceType: "artifact",
	}

	resBytes, err := hooks.ExecuteHookPipeline(m.ctx, m.app.StorageHooks.DownloadHook, &req, m.log)
	if err != nil {
		m.log.WithError(err).Error("download hook pipeline failed")
		return nil, fiber.StatusInternalServerError, errors.New("download hook pipeline failed")
	}

	// return will be using same struct
	var res hooks.DownloadHookData
	if err := json.Unmarshal(resBytes, &res); err != nil {
		return nil, fiber.StatusInternalServerError, fmt.Errorf("failed to unmarshal download hook response: %w", err)
	}

	if res.Error != "" {
		return nil, fiber.StatusInternalServerError, fmt.Errorf("download hook script returned an error: %s", res.Error)
	}

	return &res, fiber.StatusOK, nil
}

// DeleteArtifact checks permissions and deletes an artifact record and its associated file.
func (m *ArtifactModel) DeleteArtifact(req *plugnmeet.DeleteArtifactReq) error {
	artifact, err := m.ds.GetRoomArtifactByArtifactID(req.ArtifactId)
	if err != nil {
		return err
	}
	if artifact == nil {
		return config.NotFoundErr
	}

	// Check if the artifact type is deletable.
	if !m.ds.IsAllowToDeleteArtifact(plugnmeet.RoomArtifactType(artifact.Type)) {
		return fmt.Errorf("deleting '%s' artifact type is not allowed", artifact.Type)
	}

	var metadata plugnmeet.RoomArtifactMetadata
	if err := protojson.Unmarshal([]byte(artifact.Metadata), &metadata); err == nil {
		if metadata.FileInfo != nil && metadata.FileInfo.FilePath != "" {
			// If delete hook is configured, we'll use it.
			if m.app.StorageHooks != nil && len(m.app.StorageHooks.DeleteHook) > 0 {
				delReq := hooks.DeleteHookData{
					InputPath:   metadata.FileInfo.FilePath,
					ServiceType: "artifact",
				}
				resBytes, err := hooks.ExecuteHookPipeline(m.ctx, m.app.StorageHooks.DeleteHook, &delReq, m.log)
				if err != nil {
					m.log.WithError(err).Warn("delete hook pipeline failed for artifact")
				} else {
					var res hooks.DeleteHookData
					if err := json.Unmarshal(resBytes, &res); err != nil {
						m.log.WithError(err).Warn("failed to unmarshal delete hook response for artifact")
					} else if res.Error != "" {
						m.log.Warnf("delete hook script returned an error for artifact: %s", res.Error)
					}
				}
			} else {
				// Otherwise, we'll only try to delete if it's a local file.
				absolutePath := filepath.Join(*m.app.ArtifactsSettings.StoragePath, metadata.FileInfo.FilePath)
				_, err := m.MoveToTrash(absolutePath)
				if err != nil {
					m.log.WithError(err).Warn("failed to move artifact to trash")
				}
			}
		}
	}

	// Now, delete the database record.
	_, err = m.ds.DeleteArtifactByArtifactId(req.ArtifactId)
	if err != nil {
		return fmt.Errorf("failed to delete artifact from db: %w", err)
	}

	return nil
}

func (m *ArtifactModel) isDownloadable(artifactType plugnmeet.RoomArtifactType) bool {
	switch artifactType {
	case plugnmeet.RoomArtifactType_MEETING_ANALYTICS,
		plugnmeet.RoomArtifactType_MEETING_SUMMARY,
		plugnmeet.RoomArtifactType_SPEECH_TRANSCRIPTION:
		return true
	}

	return false
}
