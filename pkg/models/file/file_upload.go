package filemodel

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

type resumableUploadReq struct {
	ResumableChunkNumber      int
	ResumableTotalChunks      int
	ResumableTotalSize        int64
	ResumableIdentifier       string
	ResumableFilename         string
	ResumableCurrentChunkSize int64
}

type UploadedFileResponse struct {
	FilePath      string
	FileName      string
	FileExtension string
	FileMimeType  string
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *FileModel) ResumableFileUpload(c *fiber.Ctx) (*UploadedFileResponse, error) {
	if m.req == nil || m.req.RoomId == "" || m.req.Sid == "" {
		return nil, errors.New("RoomId or Sid is empty")
	}

	req := new(resumableUploadReq)
	res := new(UploadedFileResponse)
	err := c.QueryParser(req)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	tempFolder := os.TempDir()
	path := fmt.Sprintf("%s/%s", tempFolder, req.ResumableIdentifier)

	switch c.Method() {
	case "GET":
		// we'll check status of chunk part
		relativeChunk := fmt.Sprintf("%s%s%s%d", path, "/", "part", req.ResumableChunkNumber)

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
			room, _ := m.ds.GetRoomInfoBySid(m.req.Sid, nil)
			if room == nil || room.ID == 0 {
				_ = c.SendStatus(fiber.StatusBadRequest)
				return nil, errors.New("room isn't running")
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
		if _, err = os.Stat(path); os.IsNotExist(err) {
			err = os.MkdirAll(path, os.ModePerm)
			if err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
				log.Errorln(err)
				return nil, err
			}
		}

		relativeChunk := fmt.Sprintf("%s%s%s%d", path, "/", "part", req.ResumableChunkNumber)

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

		if req.ResumableChunkNumber == req.ResumableTotalChunks {
			// combining chunks into one file
			path, err = m.combineResumableFiles(path, req.ResumableFilename, req.ResumableTotalChunks)
			if err != nil {
				return nil, err
			}
			// we'll detect mime type again for sending data
			mtype, err := mimetype.DetectFile(path)
			if err != nil {
				return nil, err
			}
			res.FileMimeType = mtype.String()
			res.FileExtension = strings.Replace(mtype.Extension(), ".", "", 1)
		} else {
			res.FilePath = "part_uploaded"
			return res, err
		}
	}

	finalPath := fmt.Sprintf("%s/%s", m.req.Sid, req.ResumableFilename)
	res.FilePath = finalPath
	res.FileName = req.ResumableFilename

	return res, nil
}

func (m *FileModel) combineResumableFiles(chunksDir string, fileName string, totalParts int) (string, error) {
	chunkSizeInBytes := 1048576
	uploadDir := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, m.req.Sid)

	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		err = os.MkdirAll(uploadDir, os.ModePerm)
		if err != nil {
			log.Errorln(err)
		}
	}

	path := fmt.Sprintf("%s/%s", uploadDir, fileName)
	f, err := os.Create(path)
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
		return "", err
	}

	return path, nil
}
