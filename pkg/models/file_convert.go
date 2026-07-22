package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/facette/natsort"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gammazero/workerpool"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	redisservice "github.com/mynaparrot/plugnmeet-server/pkg/services/redis"
	"github.com/sirupsen/logrus"
)

const (
	SofficeTimeout = 5 * time.Minute
	MutoolTimeout  = 5 * time.Minute
	Pdf2ImgTimeout = 10 * time.Minute

	MaxMutoolWorkers    = 4
	MutoolPageChunkSize = 25
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

const (
	pageOrientationPortrait  = "portrait"
	pageOrientationLandscape = "landscape"
)

// whiteboardPageMeta is stored next to each converted page image as page_N_meta.json.
// It is uploaded with the converted images directory (local disk or storage hooks).
type whiteboardPageMeta struct {
	Page        int    `json:"page"`
	Orientation string `json:"orientation"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
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

	totalPages, err := getPDFPageCount(m.ctx, convertedFile, log)
	if err != nil {
		log.WithError(err).Error("failed to count PDF pages")
		return nil, fmt.Errorf("failed to count PDF pages")
	}

	if err := convertPDFToImages(m.ctx, convertedFile, outputDir, roomId, totalPages, log); err != nil {
		log.WithError(err).Error("failed to convert PDF to images")
		return nil, fmt.Errorf("failed to convert PDF to images")
	}

	// get actual images count
	totalPages, err = countPages(outputDir)
	if err != nil {
		log.WithError(err).Error("failed to count pages")
		return nil, fmt.Errorf("failed to count pages")
	}

	// Write page_N_meta.json beside each PNG before directory upload/hooks.
	// Client always loads orientation from these sidecars (not NATS/proto).
	if err := writePageMetaFiles(outputDir, totalPages, log); err != nil {
		log.WithError(err).Error("failed to write page meta files")
		return nil, fmt.Errorf("failed to write page meta files")
	}

	// If hooks are enabled, upload the entire directory of converted images
	// (page_N.png + page_N_meta.json).
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

// MergeWhiteboardPdfExport merges the uploaded PDF slices into a single PDF file.
func (m *FileModel) MergeWhiteboardPdfExport(req *plugnmeet.UploadedFileMergeReq, requestedUserId string) (*plugnmeet.UploadedFileRes, error) {
	req.ResumableIdentifier = helpers.MakeSafeFilename(req.ResumableIdentifier, false)
	if req.ResumableIdentifier == "" {
		return nil, fmt.Errorf("invalid or empty resumableIdentifier")
	}

	log := m.logger.WithFields(logrus.Fields{
		"roomSid":  req.RoomSid,
		"exportId": req.ResumableIdentifier,
		"method":   "MergeWhiteboardPdfExport",
	})
	log.Infoln("New request to merge whiteboard PDF export slices received")

	// The directory where all slices for this export job are stored.
	savedPath := m.getWhiteboardPdfExportSlicesDir(req.RoomSid, req.ResumableIdentifier)

	if m.app.Hooks != nil {
		hookData := &hooks.DownloadHookData{
			HookFileType: hooks.HookFileTypeFileGroup,
			RoomSid:      req.RoomSid,
			RoomId:       req.RoomId,
			GroupId:      req.ResumableIdentifier,
			InputPath:    savedPath,
		}
		hookRes, err := m.app.Hooks.RunDownloadHook(m.ctx, hookData, nil, time.Minute*10, log)
		if err != nil {
			log.WithError(err).Error("download hook failed")
			return nil, fmt.Errorf("download hook failed")
		}
		if hookRes != nil && hookRes.OutputPath != "" {
			savedPath = hookRes.OutputPath
		}
	}

	// Ensure the temporary directory exists before trying to read from it
	if _, err := os.Stat(savedPath); os.IsNotExist(err) {
		log.WithError(err).Errorf("Temporary directory %s does not exist", savedPath)
		return nil, fmt.Errorf("temporary directory for export not found")
	}

	imageFiles, err := filepath.Glob(filepath.Join(savedPath, "*.png"))
	if err != nil {
		log.WithError(err).Errorf("Failed to list PNG files in %s", savedPath)
		return nil, fmt.Errorf("failed to list image slices")
	}
	if len(imageFiles) == 0 {
		log.Warnf("No PNG files found in temporary directory: %s", savedPath)
		return nil, fmt.Errorf("no image slices found for export")
	}

	// Use natural sort for robustness and simplicity.
	// file name: pageNum_sliceNum.png e.g. 1_1.png, 1_2.png, 2_1.png, 2_2.png ...
	natsort.Sort(imageFiles)

	fileId := uuid.NewString()
	finalPdfOutputDir := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid)
	if err := os.MkdirAll(finalPdfOutputDir, 0755); err != nil {
		log.WithError(err).Errorf("Failed to create final PDF output directory: %s", finalPdfOutputDir)
		return nil, fmt.Errorf("failed to create output directory for PDF")
	}

	baseFileName := strings.TrimSuffix(req.ResumableFilename, filepath.Ext(req.ResumableFilename))
	finalPdfFileName := helpers.MakeSafeFilename("exported_"+baseFileName, true) + ".pdf"
	finalPdfPath := filepath.Join(finalPdfOutputDir, finalPdfFileName)

	// Do not force a fixed pagesize. Exported slices are already portrait or
	// landscape A4 pixel dimensions, so img2pdf should keep each image's size.
	args := []string{
		"--output", finalPdfPath,
		"--creator", "plugNmeet",
		"--title", finalPdfFileName,
		"--subject", req.ResumableFilename,
	}
	args = append(args, imageFiles...)

	timeoutCtx, cancel := context.WithTimeout(m.ctx, Pdf2ImgTimeout)
	defer cancel()

	if err := executeCommand(timeoutCtx, log, "img2pdf", args...); err != nil {
		log.WithError(err).Error("img2pdf execution failed")
		return nil, fmt.Errorf("failed to build PDF (img2pdf)")
	}

	// clean original dir
	_ = os.RemoveAll(savedPath)

	if m.app.Hooks != nil {
		uploadHookData := &hooks.UploadHookData{
			HookFileType: hooks.HookFileTypeRoomFile, // general room file
			RoomSid:      req.RoomSid,
			RoomId:       req.RoomId,
			InputPath:    finalPdfPath,
		}
		uploadRes, err := m.app.Hooks.RunUploadHook(uploadHookData, log)
		if err != nil {
			log.WithError(err).Error("upload hook pipeline failed")
			return nil, fmt.Errorf("upload hook pipeline failed")
		}
		if uploadRes != nil && uploadRes.OutputPath != "" {
			log.Infof("Successfully uploaded file into %s", uploadRes.OutputPath)
		}

		// also deleted the files by group id
		deleteHookData := &hooks.DeleteHookData{
			HookFileType: hooks.HookFileTypeFileGroup,
			RoomSid:      req.RoomSid,
			RoomId:       req.RoomId,
			GroupId:      req.ResumableIdentifier,
		}
		if _, err := m.app.Hooks.RunDeleteHook(deleteHookData, log); err != nil {
			// just log
			log.WithError(err).Error("delete hook pipeline failed")
		}
	}

	res := &plugnmeet.UploadedFileRes{
		Status:        true,
		Msg:           "success",
		FileId:        fileId,
		FilePath:      filepath.Join(req.RoomSid, finalPdfFileName),
		FileName:      finalPdfFileName,
		FileExtension: "pdf",
		FileMimeType:  "application/pdf",
	}

	meta := &plugnmeet.RoomUploadedFileMetadata{
		FileType:         plugnmeet.RoomUploadedFileType_CHAT_FILE,
		FileId:           fileId,
		FileName:         finalPdfFileName,
		FilePath:         res.FilePath,
		MimeType:         res.FileMimeType,
		UploadedByUserId: requestedUserId,
	}
	if err := m.natsService.AddRoomFile(req.RoomId, meta); err != nil {
		log.WithError(err).Error("failed to store exported PDF metadata in NATS")
		// Don't return the error, as the PDF creation was successful.
	}

	// we'll send by chat
	if err := m.natsService.BroadcastSystemEventToRoom(plugnmeet.NatsMsgServerToClientEvents_SYSTEM_CHAT_MSG, req.RoomId, meta, &requestedUserId); err != nil {
		log.WithError(err).Error("Failed to broadcast message")
	}
	log.Info("Successfully built and broadcasted whiteboard PDF export file")

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

// getPDFPageCount counts pages directly from the PDF before rendering.
func getPDFPageCount(ctx context.Context, pdfPath string, log *logrus.Entry) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, MutoolTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "mutool", "show", pdfPath, "trailer/Root/Pages/Count")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			log.Errorf("mutool page count command timed out")
			return 0, fmt.Errorf("mutool page count command timed out")
		}
		log.Errorf("mutool page count command failed: %s; output: %s", err, string(output))
		return 0, fmt.Errorf("mutool page count command failed: %w", err)
	}

	totalPages, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		log.Errorf("failed to parse PDF page count; output: %s", string(output))
		return 0, fmt.Errorf("failed to parse PDF page count")
	}

	if totalPages <= 0 {
		return 0, fmt.Errorf("invalid PDF page count: %d", totalPages)
	}

	return totalPages, nil
}

// convertPDFToImages uses mutool to convert a PDF file into PNG images.
func convertPDFToImages(ctx context.Context, pdfPath, outputDir, roomId string, totalPages int, log *logrus.Entry) error {
	log.Infof("New PDF to Image conversion request for file: %s", pdfPath)
	now := time.Now()

	ctx, cancel := context.WithTimeout(ctx, MutoolTimeout)
	defer cancel()

	workers := runtime.NumCPU()
	if workers > MaxMutoolWorkers {
		workers = MaxMutoolWorkers
	}
	log.Infof("Total pages: %d; workers: %d", totalPages, workers)

	wp := workerpool.New(workers)
	var once sync.Once
	var firstErr error

	setErr := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}

	for start := 1; start <= totalPages; start += MutoolPageChunkSize {
		end := start + MutoolPageChunkSize - 1
		if end > totalPages {
			end = totalPages
		}
		pageRange := fmt.Sprintf("%d-%d", start, end)

		wp.Submit(func() {
			err := executeCommand(ctx, log, "mutool", "draw", "-q", "-r", "300", "-o",
				filepath.Join(outputDir, "page_%d.png"), pdfPath, pageRange,
			)
			if err != nil {
				log.Errorf("mutool conversion failed for roomId: %s; file: %s; pages: %s; msg: %s", roomId, pdfPath, pageRange, err)
				setErr(fmt.Errorf("mutool: converting pages %s to images failed", pageRange))
			}
		})
	}

	wp.StopWait()

	if firstErr != nil {
		return firstErr
	}

	log.Infof("Successfully converted PDF to images in %s, Total pages: %d; workers: %d", time.Since(now), totalPages, workers)
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

// writePageMetaFiles inspects each rendered page image and writes
// page_N_meta.json next to page_N.png so multi-cluster storage hooks pick them up.
func writePageMetaFiles(outputDir string, totalPages int, log *logrus.Entry) error {
	for page := 1; page <= totalPages; page++ {
		imgPath := filepath.Join(outputDir, fmt.Sprintf("page_%d.png", page))
		width, height, err := getImageDimensions(imgPath)
		if err != nil {
			log.WithError(err).WithField("page", page).Error("failed to read page image dimensions")
			return err
		}

		orientation := pageOrientationPortrait
		if width > height {
			orientation = pageOrientationLandscape
		}

		meta := whiteboardPageMeta{
			Page:        page,
			Orientation: orientation,
			Width:       width,
			Height:      height,
		}
		metaBytes, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("failed to marshal page %d meta: %w", page, err)
		}
		metaPath := filepath.Join(outputDir, fmt.Sprintf("page_%d_meta.json", page))
		if err := os.WriteFile(metaPath, metaBytes, 0644); err != nil {
			return fmt.Errorf("failed to write page %d meta: %w", page, err)
		}
	}

	return nil
}

// getImageDimensions returns width/height without decoding full pixel data.
func getImageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
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
