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

// ConvertWhiteboardFileReq represents the request structure for converting a whiteboard file.
type ConvertWhiteboardFileReq struct {
	RoomSid  string `json:"roomSid" query:"roomSid"`
	RoomId   string `json:"roomId" query:"roomId"`
	UserId   string `json:"userId" query:"userId"`
	FilePath string `json:"filePath" query:"filePath"`
}

// ConvertWhiteboardFileRes represents the response structure after converting a whiteboard file.
type ConvertWhiteboardFileRes struct {
	Status     bool   `json:"status"`
	Msg        string `json:"msg"`
	FileName   string `json:"fileName"`
	FileId     string `json:"fileId"`
	FilePath   string `json:"filePath"`
	TotalPages int    `json:"totalPages"`
}

// ConvertAndBroadcastWhiteboardFile will convert & broadcast files for whiteboard.
func (m *FileModel) ConvertAndBroadcastWhiteboardFile(roomId, roomSid, filePath string) (*ConvertWhiteboardFileRes, error) {
	if roomId == "" || filePath == "" {
		return nil, errors.New("roomId or filePath is empty")
	}

	if err := checkDependencies(); err != nil {
		return nil, err
	}

	fullPath := filepath.Join(m.app.UploadFileSettings.Path, filePath)
	info, err := os.Stat(fullPath)
	if err != nil {
		log.Errorln("failed to stat file:", err)
		return nil, errors.New("failed to stat file")
	}

	mType, err := mimetype.DetectFile(fullPath)
	if err != nil {
		log.Errorln("failed to detect mimetype:", err)
		return nil, errors.New("mime detection failed")
	}

	fileId := uuid.NewString()
	outputDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid, fileId)
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		log.Errorln("failed to create output dir:", err)
		return nil, errors.New("failed to create output dir")
	}

	convertedFile, err := m.convertToPDFIfNeeded(fullPath, info.Name(), roomId, mType, outputDir)
	if err != nil {
		return nil, err
	}

	if err := convertPDFToImages(convertedFile, outputDir, roomId, mType.String()); err != nil {
		return nil, err
	}

	totalPages, _ := countPages(outputDir)

	res := &ConvertWhiteboardFileRes{
		Status:     true,
		Msg:        "success",
		FileName:   info.Name(),
		FilePath:   filepath.Join(roomSid, fileId),
		FileId:     fileId,
		TotalPages: totalPages,
	}

	if err := m.updateRoomMetadataWithOfficeFile(roomId, res); err != nil {
		log.Errorln("metadata update failed")
	}

	return res, nil
}

// checkDependencies verifies that required external tools are installed.
func checkDependencies() error {
	requiredPaths := []string{"/usr/bin/mutool", "/usr/bin/soffice"}
	for _, path := range requiredPaths {
		if _, err := os.Stat(path); err != nil {
			log.Errorln("required binary not found:", path)
			return errors.New("required binary not found")
		}
	}
	return nil
}

// convertToPDFIfNeeded checks if the file needs to be converted to PDF based on its MIME type.
// If conversion is necessary, it uses LibreOffice (soffice) to perform the conversion.
func (m *FileModel) convertToPDFIfNeeded(filePath, fileName, roomId string, mType *mimetype.MIME, outputDir string) (string, error) {
	ext := mType.Extension()
	var variant string
	needConvert := false

	// Determine the appropriate export variant based on file extension
	switch ext {
	case ".docx", ".doc", ".odt", ".txt", ".rtf", ".xml":
		variant = "pdf:writer_pdf_Export"
		needConvert = true
	case ".xlsx", ".xls", ".ods", ".csv":
		variant = "pdf:calc_pdf_Export"
		needConvert = true
	case ".pptx", ".ppt", ".odp":
		variant = "pdf:impress_pdf_Export"
		needConvert = true
	case ".vsd", ".odg":
		variant = "pdf:draw_pdf_Export"
		needConvert = true
	case ".html":
		variant = "pdf:writer_web_pdf_Export"
		needConvert = true
	}

	// If no conversion is necessary, return the original file path
	if !needConvert {
		return filePath, nil
	}

	// Construct the new PDF file name
	newFile := strings.Replace(fileName, ext, ".pdf", 1)

	// Execute the conversion command
	cmd := exec.Command("/usr/bin/soffice", "--headless", "--invisible", "--nologo", "--nolockcheck", "--convert-to", variant, "--outdir", outputDir, filePath)
	if _, err := cmd.Output(); err != nil {
		log.Errorln(fmt.Sprintf("soffice file conversion failed for roomId: %s; file: %s; mimeType: %s; variant: %s; msg: %s", roomId, fileName, mType.String(), variant, err.Error()))
		return "", errors.New("soffice conversion failed")
	}

	return filepath.Join(outputDir, newFile), nil
}

// convertPDFToImages uses mutool to convert a PDF file into PNG images (one per page).
func convertPDFToImages(pdfPath, outputDir, roomId, mimeType string) error {
	cmd := exec.Command("/usr/bin/mutool", "convert", "-O", "resolution=300", "-o", filepath.Join(outputDir, "page_%d.png"), pdfPath)
	if _, err := cmd.Output(); err != nil {
		log.Errorln(fmt.Sprintf("mutool file conversion failed for roomId: %s; file: %s; mimeType: %s; msg: %s", roomId, pdfPath, mimeType, err.Error()))
		return errors.New("mutool conversion failed")
	}
	return nil
}

// countPages counts the number of PNG files generated in the output directory,
// which corresponds to the number of pages in the converted document.
func countPages(outputDir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(outputDir, "*.png"))
	if err != nil {
		log.Errorln("failed to glob output dir:", err)
		return 0, errors.New("failed to count pages")
	}
	return len(files), nil
}
