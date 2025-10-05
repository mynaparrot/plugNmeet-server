package models

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *FileModel) UploadBase64EncodedData(req *plugnmeet.UploadBase64EncodedDataReq) (*plugnmeet.UploadBase64EncodedDataRes, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   req.GetRoomId(),
		"fileName": req.GetFileName(),
		"method":   "UploadBase64EncodedData",
	})

	roomInfo, err := m.ds.GetRoomInfoByRoomId(req.GetRoomId(), 1)
	if err != nil {
		log.WithError(err).Error("failed to get room info")
		return nil, err
	}
	if roomInfo == nil {
		return nil, fmt.Errorf("room is not active")
	}

	data, err := base64.StdEncoding.DecodeString(req.GetData())
	if err != nil {
		log.WithError(err).Error("failed to decode base64 data")
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

	safeFilename := filepath.Base(req.GetFileName())

	saveDir := filepath.Join(m.app.UploadFileSettings.Path, roomInfo.Sid)
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		log.WithError(err).Error("failed to create upload directory")
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	filePath := filepath.Join(saveDir, safeFilename)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		log.WithError(err).Error("failed to write file to disk")
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// at present format ${file.id}.png
	fileId := strings.TrimSuffix(req.FileName, ".png")
	meta := &plugnmeet.RoomUploadedFileMetadata{
		FileId:   fileId,
		FileName: safeFilename,
		FilePath: filePath,
		FileType: req.FileType,
		MimeType: mimeType.String(),
	}
	err = m.natsService.AddRoomFile(req.RoomId, meta)
	if err != nil {
		log.WithError(err).Error("failed to store file metadata in NATS")
	}

	//TODO: replace with UploadedFileMergeReq and set file ID
	return &plugnmeet.UploadBase64EncodedDataRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileMimeType:  mimeType.String(),
		FilePath:      filepath.Join(roomInfo.Sid, safeFilename),
		FileName:      safeFilename,
		FileExtension: strings.TrimPrefix(mimeType.Extension(), "."),
	}, nil
}
