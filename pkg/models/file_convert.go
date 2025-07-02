package models

import (
	"context"
	"errors"
	"fmt"
	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	mType, err := mimetype.DetectFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("mime detection failed: %w", err)
	}

	if err := m.ValidateMimeType(mType); err != nil {
		return nil, err
	}

	fileId := uuid.NewString()
	outputDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid, fileId)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output dir: %w", err)
	}

	convertedFile, err := m.convertToPDFIfNeeded(fullPath, info.Name(), roomId, mType, outputDir)
	if err != nil {
		return nil, err
	}

	if err := convertPDFToImages(convertedFile, outputDir, roomId); err != nil {
		return nil, err
	}

	totalPages, err := countPages(outputDir)
	if err != nil {
		return nil, err
	}

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
	for _, bin := range []string{"mutool", "soffice"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("required binary not found in PATH: %s", bin)
		}
	}
	return nil
}

// executeCommand runs a command with a timeout and handles common error cases.
func executeCommand(ctx context.Context, name string, arg ...string) error {
	cmd := exec.CommandContext(ctx, name, arg...)
	if output, err := cmd.CombinedOutput(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			log.Errorf("%s command timed out", name)
			return fmt.Errorf("%s command timed out", name)
		}
		log.Errorf("%s command failed: %s; output: %s", name, err, string(output))
		return fmt.Errorf("%s command failed: %w", name, err)
	}
	return nil
}

// convertToPDFIfNeeded checks if the file needs to be converted to PDF based on its MIME type.
// It returns the path to the PDF and an error.
func (m *FileModel) convertToPDFIfNeeded(filePath, fileName, roomId string, mType *mimetype.MIME, outputDir string) (string, error) {
	if mType.Is("application/pdf") {
		return filePath, nil
	}

	conversionMap := map[string]string{
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "pdf:writer_pdf_Export",
		"application/msword":                      "pdf:writer_pdf_Export",
		"application/vnd.oasis.opendocument.text": "pdf:writer_pdf_Export",
		"text/plain":                              "pdf:writer_pdf_Export",
		"application/rtf":                         "pdf:writer_pdf_Export",
		"application/xml":                         "pdf:writer_pdf_Export",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "pdf:calc_pdf_Export",
		"application/vnd.ms-excel":                       "pdf:calc_pdf_Export",
		"application/vnd.oasis.opendocument.spreadsheet": "pdf:calc_pdf_Export",
		"text/csv": "pdf:calc_pdf_Export",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": "pdf:impress_pdf_Export",
		"application/vnd.ms-powerpoint":                                             "pdf:impress_pdf_Export",
		"application/vnd.oasis.opendocument.presentation":                           "pdf:impress_pdf_Export",
		"application/vnd.visio":                                                     "pdf:draw_pdf_Export",
		"application/vnd.oasis.opendocument.graphics":                               "pdf:draw_pdf_Export",
		"text/html": "pdf:writer_web_pdf_Export",
	}

	variant, supported := conversionMap[mType.String()]
	if !supported {
		// This case should ideally not be reached if ValidateMimeType is comprehensive
		return "", fmt.Errorf("unsupported file type for conversion: %s", mType.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := executeCommand(ctx, "soffice", "--headless", "--invisible", "--nologo", "--nolockcheck", "--convert-to", variant, "--outdir", outputDir, filePath)
	if err != nil {
		log.Errorf("soffice conversion failed for roomId: %s; file: %s; msg: %s", roomId, fileName, err)
		return "", fmt.Errorf("soffice: converting to PDF failed")
	}

	newFile := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".pdf"
	return filepath.Join(outputDir, newFile), nil
}

// convertPDFToImages uses mutool to convert a PDF file into PNG images.
func convertPDFToImages(pdfPath, outputDir, roomId string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	err := executeCommand(ctx, "mutool", "convert", "-O", "resolution=300", "-o", filepath.Join(outputDir, "page_%d.png"), pdfPath)
	if err != nil {
		log.Errorf("mutool conversion failed for roomId: %s; file: %s; msg: %s", roomId, pdfPath, err)
		return fmt.Errorf("mutool: converting to images failed")
	}
	return nil
}

// countPages counts the number of PNG files generated in the output directory.
func countPages(outputDir string) (int, error) {
	files, err := filepath.Glob(filepath.Join(outputDir, "page_*.png"))
	if err != nil {
		return 0, fmt.Errorf("failed to count pages: %w", err)
	}
	return len(files), nil
}
