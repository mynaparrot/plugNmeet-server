package models

import (
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ManageFile struct {
	Sid                string `json:"sid"`
	RoomId             string `json:"roomId"`
	UserId             string `json:"userId"`
	FilePath           string `json:"file_path"`
	Resumable          bool   `json:"resumable"`
	uploadFileSettings *config.UploadFileSettings
	fileExtension      string
	fileMimeType       string
	rs                 *RoomService
}

func NewManageFileModel(m *ManageFile) *ManageFile {
	m.uploadFileSettings = &config.AppCnf.UploadFileSettings
	m.rs = NewRoomService()
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

type UploadedFileResponse struct {
	FilePath      string
	FileName      string
	FileExtension string
	FileMimeType  string
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *ManageFile) ResumableFileUpload(c *fiber.Ctx) (*UploadedFileResponse, error) {
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
			// we'll check if meeting is already running or not
			rm := NewRoomModel()
			room, _ := rm.GetRoomInfo(m.RoomId, m.Sid, 1)
			if room.Id == 0 {
				_ = c.SendStatus(fiber.StatusBadRequest)
				return nil, errors.New("room isn't running")
			}

			// check if file size is OK
			if req.ResumableTotalSize > int64(m.uploadFileSettings.MaxSize*1024*1024) {
				_ = c.SendStatus(fiber.StatusBadRequest)
				msg := fmt.Sprintf("file is too big. Max allow %dMB", m.uploadFileSettings.MaxSize)
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

	finalPath := fmt.Sprintf("%s/%s", m.Sid, req.ResumableFilename)
	res.FilePath = finalPath
	res.FileName = req.ResumableFilename

	return res, nil
}

func (m *ManageFile) detectMimeTypeForValidation(file multipart.File) error {
	defer file.Close()
	mtype, err := mimetype.DetectReader(file)
	if err != nil {
		return err
	}
	return m.validateMimeType(mtype)
}

func (m *ManageFile) validateMimeType(mtype *mimetype.MIME) error {
	allowedTypes := m.uploadFileSettings.AllowedTypes
	sort.Strings(allowedTypes)

	m.fileMimeType = mtype.String()
	m.fileExtension = strings.Replace(mtype.Extension(), ".", "", 1)
	allows := false

	for _, t := range allowedTypes {
		if m.fileExtension == t {
			allows = true
			continue
		}
	}
	if !allows {
		if m.fileExtension == "" {
			return errors.New("invalid file")
		}
		return errors.New(mtype.Extension() + " file type not allow")
	}

	return nil
}

func (m *ManageFile) combineResumableFiles(chunksDir string, fileName string, totalParts int) (string, error) {
	chunkSizeInBytes := 1048576
	uploadDir := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, m.Sid)

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

func (m *ManageFile) DeleteFile(filePath string) error {
	path := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, filePath)
	err := os.RemoveAll(path)
	if err != nil {
		log.Errorln(err)
	}
	return err
}

func (m *ManageFile) DeleteRoomUploadedDir() error {
	if m.Sid == "" {
		return errors.New("empty sid")
	}
	path := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, m.Sid)
	err := os.RemoveAll(path)
	if err != nil {
		log.Errorln(err)
	}
	return err
}

type convertStatus struct {
	status bool
	err    error
}
type ConvertWhiteboardFileRes struct {
	FileName   string `json:"file_name"`
	FileId     string `json:"file_id"`
	FilePath   string `json:"file_path"`
	TotalPages int    `json:"total_pages"`
}

func (m *ManageFile) ConvertWhiteboardFile() (*ConvertWhiteboardFileRes, error) {
	// check if mutool installed in correct path
	if _, err := os.Stat("/usr/bin/mutool"); err != nil {
		log.Errorln(err)
		return nil, err
	}

	file := fmt.Sprintf("%s/%s", m.uploadFileSettings.Path, m.FilePath)
	info, err := os.Stat(file)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	mtype, err := mimetype.DetectFile(file)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	fileId := uuid.NewString()
	outputDir := fmt.Sprintf("%s/%s/%s", m.uploadFileSettings.Path, m.Sid, fileId)
	err = os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	needConvertToPdf := false
	variant := "pdf:writer_pdf_Export"
	switch mtype.Extension() {
	case ".docx", ".doc", ".odt", ".txt", ".rtf", ".xml":
		needConvertToPdf = true
	case ".xlsx", ".xls", ".ods", ".csv":
		needConvertToPdf = true
		variant = "pdf:calc_pdf_Export"
	case ".pptx", ".ppt", ".odp":
		needConvertToPdf = true
		variant = "pdf:impress_pdf_Export"
	case ".vsd", ".odg":
		needConvertToPdf = true
		variant = "pdf:draw_pdf_Export"
	case ".html":
		needConvertToPdf = true
		variant = "pdf:writer_web_pdf_Export"
	}

	if needConvertToPdf {
		// check if soffice installed in correct path
		if _, err = os.Stat("/usr/bin/soffice"); err != nil {
			log.Errorln(err)
			return nil, err
		}

		newFile := strings.Replace(info.Name(), mtype.Extension(), ".pdf", 1)
		status := make(chan convertStatus)

		go func(file, variant, outputDir string) {
			cmd := exec.Command("/usr/bin/soffice", "--headless", "--invisible", "--nologo", "--nolockcheck", "--convert-to", variant, "--outdir", outputDir, file)
			_, err = cmd.Output()

			if err != nil {
				log.Errorln(err)
				status <- convertStatus{status: false, err: err}
				return
			}
			status <- convertStatus{status: true, err: nil}
		}(file, variant, outputDir)

		resStatus := <-status
		if !resStatus.status {
			return nil, resStatus.err
		}
		file = fmt.Sprintf("%s/%s", outputDir, newFile)
	}

	status := make(chan convertStatus)
	go func(file, outputDir string) {
		cmd := exec.Command("/usr/bin/mutool", "convert", "-O", "resolution=300", "-o", outputDir+"/page_%d.png", file)
		_, err = cmd.Output()

		if err != nil {
			log.Errorln(err)
			status <- convertStatus{status: false, err: err}
			return
		}
		status <- convertStatus{status: true, err: nil}
	}(file, outputDir)

	resStatus := <-status
	if !resStatus.status {
		return nil, resStatus.err
	}

	pattern := filepath.Join(outputDir, "*.png")
	totalPages, _ := filepath.Glob(pattern)

	res := &ConvertWhiteboardFileRes{
		FileName:   info.Name(),
		FilePath:   fmt.Sprintf("%s/%s", m.Sid, fileId),
		FileId:     fileId,
		TotalPages: len(totalPages),
	}

	// update metadata with info
	err = m.updateRoomMetadataWithOfficeFile(res)
	if err != nil {
		log.Errorln(err)
	}

	return res, nil
}

func (m *ManageFile) updateRoomMetadataWithOfficeFile(f *ConvertWhiteboardFileRes) error {
	_, roomMeta, err := m.rs.LoadRoomWithMetadata(m.RoomId)
	if err != nil {
		return err
	}

	roomMeta.RoomFeatures.WhiteboardFeatures.WhiteboardFileId = f.FileId
	roomMeta.RoomFeatures.WhiteboardFeatures.FileName = f.FileName
	roomMeta.RoomFeatures.WhiteboardFeatures.FilePath = f.FilePath
	roomMeta.RoomFeatures.WhiteboardFeatures.TotalPages = uint32(f.TotalPages)

	_, err = m.rs.UpdateRoomMetadataByStruct(m.RoomId, roomMeta)
	if err != nil {
		log.Errorln(err)
	}

	return err
}
