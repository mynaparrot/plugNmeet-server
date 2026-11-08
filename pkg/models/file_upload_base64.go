package models

import (
	"encoding/base64"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"
)

func (m *FileModel) UploadBase64EncodedData(req *plugnmeet.UploadBase64EncodedDataReq, roomSid string) (*plugnmeet.UploadBase64EncodedDataRes, plugnmeet.StatusCode, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":   req.GetRoomId(),
		"fileName": req.GetFileName(),
		"method":   "UploadBase64EncodedData",
	})

	data, err := base64.StdEncoding.DecodeString(req.GetData())
	if err != nil {
		log.WithError(err).Error("failed to decode base64 data")
		return nil, plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrFileInvalidRequest
	}

	// Validate file size before doing anything else
	maxSize := int64(m.app.UploadFileSettings.MaxSize * 1024 * 1024)
	if int64(len(data)) > maxSize {
		return nil, plugnmeet.StatusCode_INVALID_PARAMETERS, config.ErrFileTooLarge
	}

	// Detect mime type from memory before writing to disk
	mimeType := mimetype.Detect(data)
	if err := m.ValidateMimeType(mimeType); err != nil {
		return nil, plugnmeet.StatusCode_INVALID_PARAMETERS, err
	}

	safeFilename := helpers.MakeSafeFilename(req.GetFileName(), true)
	saveDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
	filePath := filepath.Join(saveDir, safeFilename)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		log.WithError(err).Error("failed to create upload directory")
		return nil, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrFileUploadFailed
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		log.WithError(err).Error("failed to write file to disk")
		return nil, plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, config.ErrFileUploadFailed
	}

	// at present format ${file.id}.png
	fileId := strings.TrimSuffix(safeFilename, filepath.Ext(safeFilename))
	meta := &plugnmeet.RoomUploadedFileMetadata{
		FileId:   fileId,
		FileName: safeFilename,
		FilePath: path.Join(roomSid, safeFilename),
		FileType: req.FileType,
		MimeType: mimeType.String(),
	}
	err = m.natsService.AddRoomFile(req.RoomId, meta)
	if err != nil {
		log.WithError(err).Error("failed to store file metadata in NATS")
	}

	return &plugnmeet.UploadBase64EncodedDataRes{
		Status:        true,
		Msg:           config.MsgSuccess,
		FileMimeType:  mimeType.String(),
		FilePath:      path.Join(roomSid, safeFilename),
		FileName:      safeFilename,
		FileExtension: strings.TrimPrefix(mimeType.Extension(), "."),
	}, plugnmeet.StatusCode_SUCCESS, nil
}
