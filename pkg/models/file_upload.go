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
func (m *FileModel) ResumableFileUpload(c *fiber.Ctx) (*UploadedFileResponse, error) {
	req := new(ResumableUploadReq)
	res := new(UploadedFileResponse)
	err := c.QueryParser(req)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}
	if req == nil || req.RoomId == "" || req.RoomSid == "" {
		return nil, errors.New("RoomId or RoomSid is empty")
	}

	tempFolder := fmt.Sprintf("%s/%s/tmp", m.app.UploadFileSettings.Path, req.RoomSid)
	chunkDir := fmt.Sprintf("%s/%s", tempFolder, req.ResumableIdentifier)

	switch c.Method() {
	case "GET":
		// we'll check status of chunk part
		relativeChunk := fmt.Sprintf("%s%s%s%d", chunkDir, "/", "part", req.ResumableChunkNumber)

		if _, err = os.Stat(relativeChunk); os.IsNotExist(err) {
			_ = c.SendStatus(fiber.StatusNoContent)
		} else {
			// let's check if file size is same or not
			// if not then we'll delete & re-upload again
			fileInfo, _ := os.Lstat(relativeChunk)
			if fileInfo.Size() == req.ResumableCurrentChunkSize {
				_ = c.SendStatus(fiber.StatusCreated)
			} else {
				// so, we'll delete that file.
				_ = os.RemoveAll(relativeChunk)
				_ = c.SendStatus(fiber.StatusNoContent)
			}
		}

	default:
		if req.ResumableChunkNumber == 1 {
			// we'll check if the meeting is already running or not
			room, _ := m.ds.GetRoomInfoBySid(req.RoomSid, nil)
			if room == nil || room.ID == 0 {
				_ = c.SendStatus(fiber.StatusBadRequest)
				return nil, errors.New("room is not active")
			}

			// check if the file size is OK
			if req.ResumableTotalSize > int64(m.app.UploadFileSettings.MaxSize*1024*1024) {
				_ = c.SendStatus(fiber.StatusBadRequest)
				msg := fmt.Sprintf("file is too big. Max allow %dMB", m.app.UploadFileSettings.MaxSize)
				return nil, errors.New(msg)
			}
		}

		reqf, err := c.FormFile("file")
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return nil, err
		}
		file, err := reqf.Open()
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return nil, err
		}
		defer file.Close()

		// we'll check the first one only.
		if req.ResumableChunkNumber == 1 {
			fo, _ := reqf.Open()
			err = m.detectMimeTypeForValidation(fo)
			if err != nil {
				_ = c.SendStatus(fiber.StatusUnsupportedMediaType)
				return nil, err
			}
		}

		// create path, if problem then cancel full process.
		if _, err = os.Stat(chunkDir); os.IsNotExist(err) {
			err = os.MkdirAll(chunkDir, os.ModePerm)
			if err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
				log.Errorln(err)
				return nil, err
			}
		}

		relativeChunk := fmt.Sprintf("%s%s%s%d", chunkDir, "/", "part", req.ResumableChunkNumber)

		f, err := os.OpenFile(relativeChunk, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return nil, err
		}
		defer f.Close()
		_, err = io.Copy(f, file)
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return nil, err
		}
		res.FilePath = "part_uploaded"
		return res, err
	}

	return res, nil
}

// UploadedFileMerge will combine all the parts & create final file
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

	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		err = os.MkdirAll(uploadDir, os.ModePerm)
		if err != nil {
			log.Errorln(err)
		}
	}

	combinedFile := fmt.Sprintf("%s/%s", uploadDir, fileName)
	f, err := os.Create(combinedFile)
	if err != nil {
		log.Errorf("Error: %s", err)
		return "", err
	}
	defer f.Close()

	for i := 1; i <= totalParts; i++ {
		relativePath := fmt.Sprintf("%s%s%d", chunksDir, "/part", i)
		writeOffset := int64(chunkSizeInBytes * (i - 1))

		if i == 1 {
			writeOffset = 0
		}
		dat, _ := os.ReadFile(relativePath)
		_, err = f.WriteAt(dat, writeOffset)

		if err != nil {
			log.Errorf("Error: %s", err)
		}
	}

	err = os.RemoveAll(chunksDir)
	if err != nil {
		log.Errorln(err)
	}

	return combinedFile, nil
}
