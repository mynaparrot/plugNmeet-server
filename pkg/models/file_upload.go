package models

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

type ResumableUploadReq struct {
	RoomSid                   string `json:"roomSid" query:"roomSid"`
	RoomId                    string `json:"roomId" query:"roomId"`
	UserId                    string `json:"userId" query:"userId"`
	ResumableChunkNumber      int    `query:"resumableChunkNumber"`
	ResumableTotalChunks      int    `query:"resumableTotalChunks"`
	ResumableTotalSize        int64  `query:"resumableTotalSize"`
	ResumableIdentifier       string `query:"resumableIdentifier"`
	ResumableFilename         string `query:"resumableFilename"`
	ResumableCurrentChunkSize int64  `query:"resumableCurrentChunkSize"`
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *FileModel) ResumableFileUpload(c *fiber.Ctx) (*plugnmeet.UploadedFileRes, *fiber.Error) {
	req := new(ResumableUploadReq)
	res := &plugnmeet.UploadedFileRes{
		Status: true,
	}

	if err := c.QueryParser(req); err != nil {
		m.logger.WithError(err).Errorln("failed to parse query parameters")
		return nil, fiber.NewError(fiber.StatusBadRequest, "Failed to parse query parameters")
	}
	if req.RoomId == "" || req.RoomSid == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "roomId or roomSid is empty")
	}

	// Create a logger with more context for this specific upload operation.
	log := m.logger.WithFields(logrus.Fields{
		"roomId":              req.RoomId,
		"roomSid":             req.RoomSid,
		"userId":              req.UserId,
		"resumableIdentifier": req.ResumableIdentifier,
		"resumableFilename":   req.ResumableFilename,
		"method":              "ResumableFileUpload",
	})

	tempFolder := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid, "tmp")
	chunkDir := filepath.Join(tempFolder, req.ResumableIdentifier)
	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("part%d", req.ResumableChunkNumber))

	switch c.Method() {
	case fiber.MethodGet:
		stat, err := os.Stat(chunkPath)
		if os.IsNotExist(err) {
			return res, fiber.NewError(fiber.StatusNoContent, "OK to upload")
		}
		if stat.Size() == req.ResumableCurrentChunkSize {
			res.Msg = "skipping upload as previously uploaded chunk"
			return res, fiber.NewError(fiber.StatusCreated, "skipping upload as previously uploaded chunk")
		}
		// Chunk is corrupted or size mismatch, remove it.
		_ = os.Remove(chunkPath)
		return nil, fiber.NewError(fiber.StatusNoContent, "OK to upload")

	case fiber.MethodPost:
		if req.ResumableChunkNumber == 1 {
			room, _ := m.ds.GetRoomInfoBySid(req.RoomSid, nil)
			if room == nil || room.ID == 0 {
				return nil, fiber.NewError(fiber.StatusBadRequest, "room is not active")
			}
			if req.ResumableTotalSize > int64(m.app.UploadFileSettings.MaxSize*1024*1024) {
				return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("file too large: max allowed is %dMB", m.app.UploadFileSettings.MaxSize))
			}
		}

		reqf, err := c.FormFile("file")
		if err != nil {
			log.WithError(err).Errorln("failed to get 'file' from form-data")
			return nil, fiber.NewError(fiber.StatusBadRequest, "missing 'file' in form-data")
		}

		file, err := reqf.Open()
		if err != nil {
			log.WithError(err).Errorln("failed to open multipart file header")
			return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to open uploaded file")
		}
		defer file.Close()

		if req.ResumableChunkNumber == 1 {
			if err := m.detectMimeTypeForValidation(file); err != nil {
				return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, err.Error())
			}
			// Reset reader to the beginning of the file for the next read (io.Copy)
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				log.WithError(err).Errorln("failed to reset file reader")
				return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to reset file reader")
			}
		}

		if err := os.MkdirAll(chunkDir, 0755); err != nil {
			log.WithError(err).Errorln("failed to create chunk directory")
			return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to create chunk directory")
		}

		out, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			log.WithError(err).Errorln("failed to create chunk file")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to create chunk file")
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			log.WithError(err).Errorln("failed to write chunk data")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to write chunk data")
		}

		res.FilePath = "part_uploaded"
		return res, nil
	}
	return res, nil
}

// UploadedFileMerge will combine all the parts and create a final file
func (m *FileModel) UploadedFileMerge(req *plugnmeet.UploadedFileMergeReq) (*plugnmeet.UploadedFileRes, error) {
	safeFilename := filepath.Base(req.ResumableFilename)
	tempFolder := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid, "tmp")
	chunkDir := filepath.Join(tempFolder, req.ResumableIdentifier)

	if _, err := os.Stat(chunkDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("requested file's chunks not found for identifier %s, make sure those were uploaded", req.ResumableIdentifier)
	}

	// combining chunks into one file
	combinedFile, err := m.combineResumableFiles(req, chunkDir, safeFilename)
	if err != nil {
		return nil, err
	}
	// we'll detect mime type again for sending data
	mtype, err := mimetype.DetectFile(combinedFile)
	if err != nil {
		return nil, err
	}

	finalPath := filepath.Join(req.RoomSid, safeFilename)
	fileId := uuid.NewString()
	if req.FileType != plugnmeet.RoomUploadedFileType_WHITEBOARD_CONVERTED_FILE {
		// we can save other files because this type file will process again
		// we'll save after complete conversion of that file
		meta := &plugnmeet.RoomUploadedFileMetadata{
			FileId:   fileId,
			FileName: safeFilename,
			FilePath: finalPath,
			FileType: req.FileType,
			MimeType: mtype.String(),
		}
		err = m.natsService.AddRoomFile(req.RoomId, meta)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"roomId":   req.RoomId,
				"roomSid":  req.RoomSid,
				"filePath": finalPath,
			}).WithError(err).Error("failed to store file metadata in NATS")
		}
	}

	res := &plugnmeet.UploadedFileRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileId:        fileId,
		FileType:      req.FileType,
		FileMimeType:  mtype.String(),
		FilePath:      finalPath,
		FileName:      safeFilename,
		FileExtension: strings.Replace(mtype.Extension(), ".", "", 1),
	}

	return res, nil
}

func (m *FileModel) combineResumableFiles(req *plugnmeet.UploadedFileMergeReq, chunksDir, safeFilename string) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":              req.RoomId,
		"roomSid":             req.RoomSid,
		"resumableIdentifier": req.ResumableIdentifier,
		"resumableFilename":   req.ResumableFilename,
		"fileType":            req.FileType,
		"method":              "combineResumableFiles",
	})

	uploadDir := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid)

	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.WithError(err).Errorln("failed to create upload directory")
		return "", fmt.Errorf("failed to create upload directory: %w", err)
	}

	combinedFile := filepath.Join(uploadDir, safeFilename)
	destFile, err := os.Create(combinedFile)
	if err != nil {
		log.WithError(err).Errorln("failed to create combined file")
		return "", fmt.Errorf("failed to create combined file: %w", err)
	}
	defer destFile.Close()

	var i int32 = 1
	for i = 1; i <= req.ResumableTotalChunks; i++ {
		chunkPath := filepath.Join(chunksDir, fmt.Sprintf("part%d", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			log.WithError(err).Errorf("failed to open chunk %d for merging", i)
			return "", fmt.Errorf("failed to open chunk %d: %w", i, err)
		}

		_, err = io.Copy(destFile, chunkFile)
		// Close inside the loop to free file descriptor early
		chunkFile.Close()
		if err != nil {
			log.WithError(err).Errorf("failed to write chunk %d to destination", i)
			return "", fmt.Errorf("failed to write chunk %d: %w", i, err)
		}
	}

	if err := os.RemoveAll(chunksDir); err != nil {
		log.WithError(err).Errorln("failed to remove chunk directory")
	}

	return combinedFile, nil
}
