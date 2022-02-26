package models

import (
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"mime/multipart"
	"os"
	"sort"
	"strings"
)

type ManageFile struct {
	Sid                string `json:"sid" validate:"required"`
	RoomId             string `json:"roomId" validate:"required"`
	UserId             string `json:"userId" validate:"required"`
	Resumable          bool   `json:"resumable"`
	uploadFileSettings *config.UploadFileSettings
}

func NewManageFileModel(m *ManageFile) *ManageFile {
	m.uploadFileSettings = &config.AppCnf.UploadFileSettings
	return m
}

func (m *ManageFile) CommonValidation(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if roomId == "" {
		return errors.New("no roomId in token")
	}
	if roomId != m.RoomId {
		return errors.New("token roomId & requested roomId didn't matched")
	}
	if requestedUserId != m.UserId {
		return errors.New("token UserId & requested UserId didn't matched")
	}

	return nil
}

type resumableUploadReq struct {
	ResumableChunkNumber      int
	ResumableTotalChunks      int
	ResumableTotalSize        int64
	ResumableIdentifier       string
	ResumableFilename         string
	ResumableCurrentChunkSize int64
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *ManageFile) ResumableFileUpload(c *fiber.Ctx) (string, string, error) {
	req := new(resumableUploadReq)
	err := c.QueryParser(req)
	if err != nil {
		fmt.Println(err)
		return "", "", err
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
				_ = os.Remove(relativeChunk)
				_ = c.SendStatus(fiber.StatusNoContent)
			}
		}

	default:
		if req.ResumableChunkNumber == 1 {
			// we'll check if meeting is already running or not
			rm := NewRoomModel()
			room, _ := rm.GetRoomInfo(m.RoomId, m.Sid, 1)
			if room.Id == 0 {
				_ = c.SendStatus(fiber.StatusBadRequest)
				return "", "", errors.New("room isn't running")
			}

			// check if file size is OK
			if req.ResumableTotalSize > int64(m.uploadFileSettings.MaxSize*1024*1024) {
				_ = c.SendStatus(fiber.StatusBadRequest)
				msg := fmt.Sprintf("file is too big. Max allow %dMB", m.uploadFileSettings.MaxSize)
				return "", "", errors.New(msg)
			}
		}

		reqf, err := c.FormFile("file")
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return "", "", err
		}
		file, err := reqf.Open()
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return "", "", err
		}
		defer file.Close()

		// we'll check the first one only.
		if req.ResumableChunkNumber == 1 {
			fo, _ := reqf.Open()
			err = m.validateMimeType(fo)
			if err != nil {
				_ = c.SendStatus(fiber.StatusUnsupportedMediaType)
				return "", "", err
			}
		}

		// create path, if problem then cancel full process.
		if _, err = os.Stat(path); os.IsNotExist(err) {
			err = os.MkdirAll(path, os.ModePerm)
			if err != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
				log.Errorln(err)
				return "", "", err
			}
		}

		relativeChunk := fmt.Sprintf("%s%s%s%d", path, "/", "part", req.ResumableChunkNumber)

		f, err := os.OpenFile(relativeChunk, os.O_WRONLY|os.O_CREATE, 0666)
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return "", "", err
		}
		defer f.Close()
		_, err = io.Copy(f, file)
		if err != nil {
			log.Errorln(err)
			_ = c.SendStatus(fiber.StatusServiceUnavailable)
			return "", "", err
		}

		if req.ResumableChunkNumber == req.ResumableTotalChunks {
			// combining chunks into one file
			err = m.combineResumableFiles(path, req.ResumableFilename, req.ResumableTotalChunks)
			if err != nil {
				return "", "", err
			}
		} else {
			return "part_uploaded", "", err
		}
	}

	finalPath := fmt.Sprintf("%s/%s", m.Sid, req.ResumableFilename)
	return finalPath, req.ResumableFilename, nil
}

func (m *ManageFile) validateMimeType(file multipart.File) error {
	defer file.Close()
	mtype, err := mimetype.DetectReader(file)
	if err != nil {
		fmt.Println(err)
		return err
	}

	allowedTypes := m.uploadFileSettings.AllowedTypes
	sort.Strings(allowedTypes)
	ext := strings.Replace(mtype.Extension(), ".", "", 1)
	allows := false

	for _, t := range allowedTypes {
		if ext == t {
			allows = true
			continue
		}
	}
	if !allows {
		if ext == "" {
			return errors.New("invalid file")
		}
		return errors.New(mtype.Extension() + " file type not allow")
	}

	return nil
}

func (m *ManageFile) combineResumableFiles(chunksDir string, fileName string, totalParts int) error {
	chunkSizeInBytes := 1048576
	uploadDir := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, m.Sid)

	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		_ = os.MkdirAll(uploadDir, os.ModePerm)
	}

	path := fmt.Sprintf("%s/%s", uploadDir, fileName)
	f, err := os.Create(path)
	if err != nil {
		fmt.Printf("Error: %s", err)
		return err
	}
	defer f.Close()

	for i := 1; i <= totalParts; i++ {
		relativePath := fmt.Sprintf("%s%s%d", chunksDir, "/part", i)
		writeOffset := int64(chunkSizeInBytes * (i - 1))

		if i == 1 {
			writeOffset = 0
		}
		dat, _ := ioutil.ReadFile(relativePath)
		_, err = f.WriteAt(dat, writeOffset)

		if err != nil {
			fmt.Printf("Error: %s", err)
		}
	}

	err = os.RemoveAll(chunksDir)
	if err != nil {
		fmt.Println(err)
		return err
	}

	return nil
}

func (m *ManageFile) DeleteFile(filePath string) error {
	path := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, filePath)
	return os.Remove(path)
}

func (m *ManageFile) DeleteRoomUploadedDir() error {
	path := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, m.Sid)
	return os.RemoveAll(path)
}
