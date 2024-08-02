package filemodel

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

type ConvertWhiteboardFileRes struct {
	FileName   string `json:"file_name"`
	FileId     string `json:"file_id"`
	FilePath   string `json:"file_path"`
	TotalPages int    `json:"total_pages"`
}

func (m *FileModel) ConvertWhiteboardFile() (*ConvertWhiteboardFileRes, error) {
	if m.req == nil || m.req.RoomId == "" || m.req.Sid == "" {
		return nil, errors.New("RoomId or Sid is empty")
	}

	// check if mutool installed in correct path
	if _, err := os.Stat("/usr/bin/mutool"); err != nil {
		log.Errorln(err)
		return nil, err
	}

	file := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, m.req.FilePath)
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
	outputDir := fmt.Sprintf("%s/%s/%s", m.app.UploadFileSettings.Path, m.req.Sid, fileId)
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
		FilePath:   fmt.Sprintf("%s/%s", m.req.Sid, fileId),
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
