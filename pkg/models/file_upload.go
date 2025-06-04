package models

import (
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"strings"
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
		log.Errorln(err)
		return nil, fiber.NewError(fiber.StatusBadRequest, "Failed to parse query parameters")
	}
	if req.RoomId == "" || req.RoomSid == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "roomId or roomSid is empty")
	}

	tempFolder := fmt.Sprintf("%s/%s/tmp", m.app.UploadFileSettings.Path, req.RoomSid)
	chunkDir := fmt.Sprintf("%s/%s", tempFolder, req.ResumableIdentifier)
	chunkPath := fmt.Sprintf("%s/part%d", chunkDir, req.ResumableChunkNumber)

	switch c.Method() {
	case fiber.MethodGet:
		if stat, err := os.Stat(chunkPath); os.IsNotExist(err) {
			return res, fiber.NewError(fiber.StatusNoContent, "OK to upload")
		} else if stat != nil && stat.Size() == req.ResumableCurrentChunkSize {
			return res, fiber.NewError(fiber.StatusCreated, "skipping upload as previously uploaded chunk")
		} else {
			_ = os.Remove(chunkPath)
			return nil, fiber.NewError(fiber.StatusNoContent, "OK to upload")
		}

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
			log.Errorln(err)
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to get form file")
		}

		file, err := reqf.Open()
		if err != nil {
			log.Errorln(err)
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to open uploaded file")
		}
		defer file.Close()

		if req.ResumableChunkNumber == 1 {
			fo, _ := reqf.Open()
			defer fo.Close()
			if err := m.detectMimeTypeForValidation(fo); err != nil {
				return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, err.Error())
			}
		}

		if err := os.MkdirAll(chunkDir, os.ModePerm); err != nil {
			log.Errorln(err)
			return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to create chunk directory")
		}

		out, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			log.Errorln(err)
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to create chunk file")
		}
		defer out.Close()

		if _, err := io.Copy(out, file); err != nil {
			log.Errorln(err)
			return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to write chunk data")
		}

		res.FilePath = "part_uploaded"
		return res, nil
	}
	return res, nil
}

// UploadedFileMerge will combine all the parts and create a final file
func (m *FileModel) UploadedFileMerge(req *ResumableUploadedFileMergeReq) (*UploadedFileResponse, error) {
	tempFolder := fmt.Sprintf("%s/%s/tmp", m.app.UploadFileSettings.Path, req.RoomSid)
	chunkDir := fmt.Sprintf("%s/%s", tempFolder, req.ResumableIdentifier)

	if _, err := os.Stat(chunkDir); os.IsNotExist(err) {
		return nil, errors.New("requested file's chunks not found, make sure those were uploaded")
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

	finalPath := fmt.Sprintf("%s/%s", req.RoomSid, req.ResumableFilename)
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
	chunkSizeInBytes := 1048576
	uploadDir := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomSid)

	if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
		log.Errorln(err)
		return "", errors.New("failed to create upload directory")
	}

	combinedFile := fmt.Sprintf("%s/%s", uploadDir, fileName)
	f, err := os.Create(combinedFile)
	if err != nil {
		log.Errorln(err)
		return "", errors.New("failed to create combined file")
	}
	defer f.Close()

	for i := 1; i <= totalParts; i++ {
		relativePath := fmt.Sprintf("%s/part%d", chunksDir, i)
		writeOffset := int64(chunkSizeInBytes * (i - 1))

		dat, err := os.ReadFile(relativePath)
		if err != nil {
			log.Errorf("failed to read chunk %d", i)
			return "", errors.New("failed to read chunk")
		}

		if _, err := f.WriteAt(dat, writeOffset); err != nil {
			log.Errorln("failed to write chunk %d", i)
			return "", errors.New("failed to write chunk")
		}
	}

	if err := os.RemoveAll(chunksDir); err != nil {
		log.Errorln("failed to remove chunk directory")
	}

	return combinedFile, nil
}
