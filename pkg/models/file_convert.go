package models

import (
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type convertStatus struct {
	status bool
	err    error
}

type ConvertWhiteboardFileReq struct {
	RoomSid  string `json:"roomSid" query:"roomSid"`
	RoomId   string `json:"roomId" query:"roomId"`
	UserId   string `json:"userId" query:"userId"`
	FilePath string `json:"filePath" query:"filePath"`
}

type ConvertWhiteboardFileRes struct {
	Status     bool   `json:"status"`
	Msg        string `json:"msg"`
	FileName   string `json:"fileName"`
	FileId     string `json:"fileId"`
	FilePath   string `json:"filePath"`
	TotalPages int    `json:"totalPages"`
}

// ConvertAndBroadcastWhiteboardFile will convert & broadcast files for whiteboard
func (m *FileModel) ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath string) (*ConvertWhiteboardFileRes, error) {
	if roomId == "" || filePath == "" {
		return nil, errors.New("roomId or file path is empty")
	}

	// check if mutool installed in the correct path
	if _, err := os.Stat("/usr/bin/mutool"); err != nil {
		log.Errorln(err)
		return nil, err
	}

	file := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, filePath)
	info, err := os.Stat(file)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	mType, err := mimetype.DetectFile(file)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	fileId := uuid.NewString()
	outputDir := fmt.Sprintf("%s/%s/%s", m.app.UploadFileSettings.Path, roomSid, fileId)
	err = os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {
		log.Errorln(err)
		return nil, err
	}

	needConvertToPdf := false
	variant := "pdf:writer_pdf_Export"
	switch mType.Extension() {
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
		// check if soffice installed in the correct path
		if _, err = os.Stat("/usr/bin/soffice"); err != nil {
			log.Errorln(err)
			return nil, err
		}

		newFile := strings.Replace(info.Name(), mType.Extension(), ".pdf", 1)
		status := make(chan convertStatus)

		go func(file, variant, outputDir string) {
			cmd := exec.Command("/usr/bin/soffice", "--headless", "--invisible", "--nologo", "--nolockcheck", "--convert-to", variant, "--outdir", outputDir, file)
			_, err = cmd.Output()

			if err != nil {
				log.Errorln(fmt.Sprintf("soffice file conversion failed for roomId: %s; file: %s; mimeType: %s; variant: %s; msg: %s", roomId, file, mType.String(), variant, err.Error()))
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
			log.Errorln(fmt.Sprintf("mutool file conversion failed for roomId: %s; file: %s; mimeType: %s; msg: %s", roomId, file, mType.String(), err.Error()))
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
		Status:     true,
		Msg:        "success",
		FileName:   info.Name(),
		FilePath:   fmt.Sprintf("%s/%s", roomSid, fileId),
		FileId:     fileId,
		TotalPages: len(totalPages),
	}

	// update metadata with info
	err = m.updateRoomMetadataWithOfficeFile(roomId, res)
	if err != nil {
		log.Errorln(err)
	}

	return res, nil
}
