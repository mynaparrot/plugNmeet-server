package models

import (
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"os"
	"path/filepath"
	"strings"
)

func (m *FileModel) UploadBase64EncodedData(req *plugnmeet.UploadBase64EncodedDataReq) (*plugnmeet.UploadBase64EncodedDataRes, error) {
	roomInfo, err := m.ds.GetRoomInfoByRoomId(req.GetRoomId(), 1)
	if err != nil {
		return nil, err
	}
	if roomInfo == nil {
		return nil, errors.New("room is not active")
	}

	data, err := base64.StdEncoding.DecodeString(req.GetData())
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 data: %w", err)
	}

	// Validate file size before doing anything else
	maxSize := int64(m.app.UploadFileSettings.MaxSize * 1024 * 1024)
	if int64(len(data)) > maxSize {
		return nil, fmt.Errorf("file too large: max allowed is %dMB", m.app.UploadFileSettings.MaxSize)
	}

	// Detect mime type from memory before writing to disk
	mimeType := mimetype.Detect(data)
	if err := m.ValidateMimeType(mimeType); err != nil {
		return nil, err
	}

	saveDir := filepath.Join(m.app.UploadFileSettings.Path, roomInfo.Sid)
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	filePath := filepath.Join(saveDir, req.GetFileName())
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	return &plugnmeet.UploadBase64EncodedDataRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileMimeType:  mimeType.String(),
		FilePath:      filepath.Join(roomInfo.Sid, req.GetFileName()),
		FileName:      req.GetFileName(),
		FileExtension: strings.TrimPrefix(mimeType.Extension(), "."),
	}, nil
}
