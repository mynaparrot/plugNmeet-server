package models

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"google.golang.org/protobuf/encoding/protojson"
)

// FetchArtifacts fetches records from the DB and formats them for the API response.
func (m *ArtifactModel) FetchArtifacts(req *plugnmeet.FetchArtifactsReq) (*plugnmeet.FetchArtifactsResult, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	} else if req.Limit > 100 {
		req.Limit = 100
	}

	dbArtifacts, total, err := m.ds.GetArtifacts(req.RoomIds, req.RoomSid, req.Type, req.From, req.Limit, &req.OrderBy)
	if err != nil {
		return nil, err
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

func (m *ArtifactModel) GetArtifactDetails(artifactId string) (*plugnmeet.ArtifactDetailsRes, error) {
	dbArtifact, roomInfo, err := m.ds.GetRoomArtifactDetails(artifactId)
	if err != nil {
		return nil, err
	}

	metadata := new(plugnmeet.RoomArtifactMetadata)
	err = protojson.Unmarshal([]byte(dbArtifact.Metadata), metadata)
	if err != nil {
		return nil, err
	}

	res := &plugnmeet.ArtifactDetailsRes{
		Status: true,
		Msg:    "success",
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
		return "", fmt.Errorf("artifact not found with ID: %s", req.ArtifactId)
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

// VerifyArtifactDownloadJWT validates a JWT and returns the file's absolute path and name.
func (m *ArtifactModel) VerifyArtifactDownloadJWT(token string) (string, string, error) {
	tok, err := jwt.ParseSigned(token, []jose.SignatureAlgorithm{jose.HS256})
	if err != nil {
		return "", "", err
	}

	out := jwt.Claims{}
	if err = tok.Claims([]byte(m.app.Client.Secret), &out); err != nil {
		return "", "", err
	}

	if err = out.Validate(jwt.Expected{
		Issuer: m.app.Client.ApiKey,
		Time:   time.Now().UTC(),
	}); err != nil {
		return "", "", err
	}

	relativePath := out.Subject
	if relativePath == "" {
		return "", "", errors.New("invalid token: file path not found")
	}

	absolutePath := filepath.Join(*m.app.ArtifactsSettings.StoragePath, relativePath)
	f, err := os.Lstat(absolutePath)
	if err != nil {
		ms := strings.SplitN(err.Error(), "/", -1)
		return "", "", errors.New(ms[len(ms)-1])
	}

	return absolutePath, f.Name(), nil
}

// DeleteArtifact checks permissions and deletes an artifact record and its associated file.
func (m *ArtifactModel) DeleteArtifact(req *plugnmeet.DeleteArtifactReq) error {
	artifact, err := m.ds.GetRoomArtifactByArtifactID(req.ArtifactId)
	if err != nil {
		return err
	}
	if artifact == nil {
		return fmt.Errorf("artifact not found with ID: %s", req.ArtifactId)
	}

	// Check if the artifact type is deletable.
	if !m.ds.IsAllowToDeleteArtifact(plugnmeet.RoomArtifactType(artifact.Type)) {
		return fmt.Errorf("deleting '%s' artifact type is not allowed", artifact.Type)
	}

	var metadata plugnmeet.RoomArtifactMetadata
	if err := protojson.Unmarshal([]byte(artifact.Metadata), &metadata); err == nil {
		if metadata.FileInfo != nil && metadata.FileInfo.FilePath != "" {
			absolutePath := filepath.Join(*m.app.ArtifactsSettings.StoragePath, metadata.FileInfo.FilePath)
			// Move the file to the trash.
			_, err := m.MoveToTrash(absolutePath)
			if err != nil {
				// Log the error but don't block the DB deletion.
				m.log.WithError(err).Warn("failed to move artifact to trash")
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
