package models

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
)

type ResumableUploadReq struct {
	RoomSid                   string `json:"roomSid" query:"roomSid"`
	RoomId                    string `json:"roomId" query:"roomId"`
	UserId                    string `json:"userId" query:"userId"`
	ResumableChunkNumber      int    `query:"resumableChunkNumber"`
	ResumableTotalChunks      int    `query:"resumableTotalChunks"`
	ResumableTotalSize        int64  `query:"resumableChunkSize"`
	ResumableIdentifier       string `query:"resumableIdentifier"`
	ResumableFilename         string `query:"resumableFilename"`
	ResumableCurrentChunkSize int64  `query:"resumableCurrentChunkSize"`
}

type ResumableUploadedFileMergeReq struct {
	RoomSid              string `json:"roomSid" query:"roomSid"`
	RoomId               string `json:"roomId" query:"roomId"`
	ResumableIdentifier  string `json:"resumableIdentifier" query:"resumableIdentifier"`
	ResumableFilename    string `json:"resumableFilename" query:"resumableFilename"`
	ResumableTotalChunks int    `json:"resumableTotalChunks" query:"resumableTotalChunks"`
}

type UploadedFileResponse struct {
	Status        bool   `json:"status"`
	Msg           string `json:"msg"`
	FilePath      string `json:"filePath"`
	FileName      string `json:"fileName"`
	FileExtension string `json:"fileExtension"`
	FileMimeType  string `json:"fileMimeType"`
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *FileModel) ResumableFileUpload(c *fiber.Ctx) (*UploadedFileResponse, *fiber.Error) {
	req := new(ResumableUploadReq)
	res := &UploadedFileResponse{
		Status: true,
	}

	if err := c.QueryParser(req); err != nil {
		m.logger.WithError(err).Errorln("failed to parse query parameters")
		return nil, fiber.NewError(fiber.StatusBadRequest, "Failed to parse query parameters")
	}
	if req.RoomId == "" || req.RoomSid == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "roomId or roomSid is empty")
	}

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
			m.logger.WithError(err).Errorln("failed to get form file")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to get form file")
		}

		file, err := reqf.Open()
		if err != nil {
			m.logger.WithError(err).Errorln("failed to open uploaded file")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to open uploaded file")
		}
		defer file.Close()

		if req.ResumableChunkNumber == 1 {
			if err := m.detectMimeTypeForValidation(file); err != nil {
				return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, err.Error())
			}
			// Reset reader to the beginning of the file for the next read (io.Copy)
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				m.logger.WithError(err).Errorln("failed to reset file reader")
				return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to reset file reader")
			}
		}

		if err := os.MkdirAll(chunkDir, 0755); err != nil {
			m.logger.WithError(err).Errorln("failed to create chunk directory")
			return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to create chunk directory")
		}

		out, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			m.logger.WithError(err).Errorln("failed to create chunk file")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to create chunk file")
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			m.logger.WithError(err).Errorln("failed to write chunk data")
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to write chunk data")
		}

		res.FilePath = "part_uploaded"
		return res, nil
	}
	return res, nil
}

// UploadedFileMerge will combine all the parts and create a final file
func (m *FileModel) UploadedFileMerge(req *ResumableUploadedFileMergeReq) (*UploadedFileResponse, error) {
	tempFolder := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid, "tmp")
	chunkDir := filepath.Join(tempFolder, req.ResumableIdentifier)

	if _, err := os.Stat(chunkDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("requested file's chunks not found, make sure those were uploaded")
	}

	// combining chunks into one file
	combinedFile, err := m.combineResumableFiles(chunkDir, req.ResumableFilename, req.RoomSid, req.ResumableTotalChunks)
	if err != nil {
		return nil, err
	}
	// we'll detect mime type again for sending data
	mtype, err := mimetype.DetectFile(combinedFile)
	if err != nil {
		return nil, err
	}

	finalPath := filepath.Join(req.RoomSid, req.ResumableFilename)
	res := &UploadedFileResponse{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileMimeType:  mtype.String(),
		FilePath:      finalPath,
		FileName:      req.ResumableFilename,
		FileExtension: strings.Replace(mtype.Extension(), ".", "", 1),
	}

	return res, nil
}

func (m *FileModel) combineResumableFiles(chunksDir, fileName, roomSid string, totalParts int) (string, error) {
	uploadDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)

	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		m.logger.Errorln(err)
		return "", fmt.Errorf("failed to create upload directory")
	}

	combinedFile := filepath.Join(uploadDir, fileName)
	destFile, err := os.Create(combinedFile)
	if err != nil {
		m.logger.Errorln(err)
		return "", fmt.Errorf("failed to create combined file")
	}
	defer destFile.Close()

	for i := 1; i <= totalParts; i++ {
		chunkPath := filepath.Join(chunksDir, fmt.Sprintf("part%d", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			m.logger.WithError(err).Errorf("failed to open chunk %d", i)
			return "", fmt.Errorf("failed to open chunk %d", i)
		}

		_, err = io.Copy(destFile, chunkFile)
		// Close inside the loop to free file descriptor early
		chunkFile.Close()
		if err != nil {
			m.logger.WithError(err).Errorf("failed to write chunk %d", i)
			return "", fmt.Errorf("failed to write chunk %d", i)
		}
	}

	if err := os.RemoveAll(chunksDir); err != nil {
		m.logger.WithError(err).Errorln("failed to remove chunk directory")
	}

	return combinedFile, nil
}
