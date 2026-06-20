package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	SofficeTimeout = 5 * time.Minute
	MutoolTimeout  = 5 * time.Minute
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

// conversionResult is a private struct to pass results over a channel.
type conversionResult struct {
	res *ConvertWhiteboardFileRes
	err error
}

// ConvertAndBroadcastWhiteboardFile starts a file conversion and waits for the result up to the context's timeout.
// If the timeout is exceeded, it returns ErrConversionTimeout, but the background process continues.
func (m *FileModel) ConvertAndBroadcastWhiteboardFile(ctx context.Context, roomId, roomSid, filePath string, requestedUserId *string, lock *redisservice.Lock, log *logrus.Entry) (*ConvertWhiteboardFileRes, error) {
	log = m.logger.WithFields(logrus.Fields{
		"roomId":     roomId,
		"roomSid":    roomSid,
		"filePath":   filePath,
		"sub-method": "ConvertAndBroadcastWhiteboardFile",
	})
	resultChan := make(chan conversionResult, 1)

	// Run the conversion in a goroutine.
	go func() {
		if lock != nil {
			defer lock.Unlock(context.Background())
		}
		res, err := m.processAndBroadcastWhiteboardFile(roomId, roomSid, filePath, requestedUserId, log)
		resultChan <- conversionResult{res, err}
	}()

	select {
	case result := <-resultChan:
		// Conversion finished in time. Return the result directly.
		return result.res, result.err
	case <-ctx.Done():
		// The handler's timeout was reached.
		log.WithFields(logrus.Fields{
			"roomId":   roomId,
			"filePath": filePath,
		}).Infoln("handler timeout reached, conversion will continue in background")
		return nil, config.ErrConversionTimeout
	}
}

// processAndBroadcastWhiteboardFile contains the original, unmodified conversion logic.
func (m *FileModel) processAndBroadcastWhiteboardFile(roomId, roomSid, filePath string, requestedUserId *string, log *logrus.Entry) (res *ConvertWhiteboardFileRes, err error) {
	log = m.logger.WithField("sub-method", "processAndBroadcastWhiteboardFile")
	log.Infoln("New request to convert and broadcast whiteboard file received")

	if roomId == "" || filePath == "" {
		err := errors.New("roomId or filePath is empty")
		log.WithError(err).Error()
		return nil, err
	}

	if err := checkDependencies(); err != nil {
		log.WithError(err).Error("dependency check failed")
		return nil, err
	}

	var fullPath string

	// If hooks are enabled, we need to download the file first.
	if m.app.Hooks != nil {
		req := hooks.DownloadHookData{
			InputPath:    filePath,
			HookFileType: hooks.HookFileTypeRoomFile,
		}
		outputDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid)
		downloadRes, err := m.app.Hooks.RunDownloadHook(m.ctx, &req, &outputDir, time.Minute*3, log)
		if err != nil {
			return nil, err
		}
		if downloadRes != nil && downloadRes.OutputPath != "" {
			fullPath = downloadRes.OutputPath
		}
	}

	if fullPath == "" {
		// fallback to default
		fullPath = filepath.Join(m.app.UploadFileSettings.Path, filePath)
	}

	fileName := filepath.Base(fullPath)
	mType, err := mimetype.DetectFile(fullPath)
	if err != nil {
		log.WithError(err).Error("mime detection failed")
		return nil, fmt.Errorf("mime detection failed")
	}

	if err := m.ValidateMimeType(mType); err != nil {
		log.WithError(err).Error("mime type validation failed")
		return nil, fmt.Errorf("mime type validation failed")
	}

	fileId := uuid.NewString()
	outputDir := filepath.Join(m.app.UploadFileSettings.Path, roomSid, fileId)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.WithError(err).Error("failed to create output dir")
		return nil, fmt.Errorf("failed to create output dir")
	}

	defer func() {
		if err != nil {
			_ = os.RemoveAll(outputDir)
		}
	}()

	convertedFile, err := m.convertToPDFIfNeeded(fullPath, fileName, roomId, mType, outputDir, log)
	if err != nil {
		log.WithError(err).Error("failed to convert file to PDF")
		return nil, fmt.Errorf("failed to convert file to PDF")
	}

	if err := convertPDFToImages(m.ctx, convertedFile, outputDir, roomId, log); err != nil {
		log.WithError(err).Error("failed to convert PDF to images")
		return nil, fmt.Errorf("failed to convert PDF to images")
	}

	totalPages, err := countPages(outputDir)
	if err != nil {
		log.WithError(err).Error("failed to count pages")
		return nil, fmt.Errorf("failed to count pages")
	}

	// If hooks are enabled, upload the entire directory of converted images.
	if m.app.Hooks != nil {
		req := hooks.UploadHookData{
			InputDirectoryPath: outputDir,
			FileId:             fileId,
			HookFileType:       hooks.HookFileTypeWhiteboardConvertedImgs,
			RoomId:             roomId,
			RoomSid:            roomSid,
		}
		uploadRes, err := m.app.Hooks.RunUploadHook(&req, log)
		if err != nil {
			log.WithError(err).Error("upload hook pipeline for converted images failed")
			return nil, fmt.Errorf("upload hook pipeline for converted images failed")
		}
		if uploadRes != nil && uploadRes.OutputPath != "" {
			log.Infof("Successfully uploaded images into %s", uploadRes.OutputPath)
		}
	}

	res = &ConvertWhiteboardFileRes{
		Status:     true,
		Msg:        "success",
		FileName:   fileName,
		FilePath:   filepath.Join(roomSid, fileId),
		FileId:     fileId,
		TotalPages: totalPages,
	}

	if err := m.addFileToNatsStore(roomId, res); err != nil {
		log.WithError(err).Error("failed to store converted file metadata in NATS")
		// Don't return the error, as the file conversion was successful.
	}

	// send notification about new file
	if requestedUserId == nil {
		// because only present have whiteboard file upload/manage capability
		if presenterId, err := m.userModel.FindCurrentPresenter(roomId); err != nil {
			log.WithError(err).Error("failed to find presenter")
		} else {
			requestedUserId = &presenterId
		}
	}
	if requestedUserId != nil {
		if err := m.natsService.BroadcastSystemNotificationToRoom(roomId, "notifications.whiteboard-new-file-added", plugnmeet.NatsSystemNotificationTypes_NATS_SYSTEM_NOTIFICATION_INFO, true, requestedUserId); err != nil {
			log.WithError(err).Error("failed to broadcast notification")
		}
	}

	log.WithField("totalPages", totalPages).Info("Successfully converted and broadcasted whiteboard file")
	return res, nil
}

// addFileToNatsStore stores the metadata of a converted file into the dedicated NATS KV bucket.
func (m *FileModel) addFileToNatsStore(roomId string, fileInfo *ConvertWhiteboardFileRes) error {
	meta := plugnmeet.RoomUploadedFileMetadata{
		FileId:     fileInfo.FileId,
		FileName:   fileInfo.FileName,
		FilePath:   fileInfo.FilePath,
		FileType:   plugnmeet.RoomUploadedFileType_WHITEBOARD_CONVERTED_FILE,
		TotalPages: new(int32(fileInfo.TotalPages)),
	}
	return m.natsService.AddRoomFile(roomId, &meta)
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
func executeCommand(ctx context.Context, log *logrus.Entry, name string, arg ...string) error {
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

// getFileExtension is a helper to normalize and return the file extension.
func getFileExtension(mime *mimetype.MIME) string {
	// Use the official extension if available.
	ext := mime.Extension()
	if ext != "" {
		return ext
	}
	// Fallback for common cases not covered by the library.
	switch {
	case mime.Is("application/vnd.openxmlformats-officedocument.wordprocessingml.document"):
		return ".docx"
	case mime.Is("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"):
		return ".xlsx"
	case mime.Is("application/vnd.openxmlformats-officedocument.presentationml.presentation"):
		return ".pptx"
	}
	return ""
}

// convertToPDFIfNeeded checks if the file needs to be converted to PDF based on its MIME type.
// It returns the path to the PDF and an error.
func (m *FileModel) convertToPDFIfNeeded(filePath, fileName, roomId string, mime *mimetype.MIME, outputDir string, log *logrus.Entry) (string, error) {
	if mime.Is("application/pdf") {
		return filePath, nil
	}
	ext := getFileExtension(mime)

	// Map file extensions to soffice conversion variants.
	conversionMap := map[string]string{
		// Word processing documents
		".docx": "pdf:writer_pdf_Export",
		".doc":  "pdf:writer_pdf_Export",
		".odt":  "pdf:writer_pdf_Export",
		".txt":  "pdf:writer_pdf_Export",
		".rtf":  "pdf:writer_pdf_Export",
		".xml":  "pdf:writer_pdf_Export",
		".html": "pdf:writer_web_pdf_Export",

		// Spreadsheet documents
		".xlsx": "pdf:calc_pdf_Export",
		".xls":  "pdf:calc_pdf_Export",
		".ods":  "pdf:calc_pdf_Export",
		".csv":  "pdf:calc_pdf_Export",

		// Presentation documents
		".pptx": "pdf:impress_pdf_Export",
		".ppt":  "pdf:impress_pdf_Export",
		".odp":  "pdf:impress_pdf_Export",

		// Drawing documents
		".vsd": "pdf:draw_pdf_Export",
		".odg": "pdf:draw_pdf_Export",
	}

	variant, supported := conversionMap[ext]
	if !supported {
		return "", fmt.Errorf("unsupported file type for conversion: %s", ext)
	}
	log.WithFields(logrus.Fields{
		"extension": ext,
		"variant":   variant,
	}).Infof("New Doc to PDF conversion request for file: %s", filePath)

	ctx, cancel := context.WithTimeout(m.ctx, SofficeTimeout)
	defer cancel()

	err := executeCommand(ctx, log, "soffice", "--headless", "--invisible", "--nologo", "--nolockcheck", "--convert-to", variant, "--outdir", outputDir, filePath)
	if err != nil {
		log.Errorf("soffice conversion failed for roomId: %s; file: %s; msg: %s", roomId, fileName, err)
		return "", fmt.Errorf("soffice: converting to PDF failed")
	}

	newFile := strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".pdf"
	return filepath.Join(outputDir, newFile), nil
}

// convertPDFToImages uses mutool to convert a PDF file into PNG images.
func convertPDFToImages(ctx context.Context, pdfPath, outputDir, roomId string, log *logrus.Entry) error {
	log.Infof("New PDF to Image conversion request for file: %s", pdfPath)
	ctx, cancel := context.WithTimeout(ctx, MutoolTimeout)
	defer cancel()

	err := executeCommand(ctx, log, "mutool", "draw", "-r", "300", "-o", filepath.Join(outputDir, "page_%d.png"), pdfPath)
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
