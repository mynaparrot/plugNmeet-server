package models

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"github.com/sirupsen/logrus"
)

type ResumableUploadReq struct {
	RoomSid                   string `json:"roomSid" query:"roomSid"`
	RoomId                    string `json:"roomId" query:"roomId"`
	UserId                    string `json:"userId" query:"userId"`
	ResumableChunkNumber      int    `query:"resumableChunkNumber"`
	ResumableTotalChunks      int    `query:"resumableTotalChunks"`
	ResumableTotalSize        int64  `query:"resumableTotalSize"`
	ResumableIdentifier       string `query:"resumableIdentifier"`
	ResumableFilename         string `query:"resumableFilename"`
	ResumableCurrentChunkSize int64  `query:"resumableCurrentChunkSize"`
}

// ResumableFileUpload method can only be use if you are using resumable.js as your frontend.
// Library link: https://github.com/23/resumable.js
func (m *FileModel) ResumableFileUpload(c fiber.Ctx, req *ResumableUploadReq) (*plugnmeet.UploadedFileRes, *fiber.Error) {
	res := &plugnmeet.UploadedFileRes{
		Status: true,
	}

	safeIdentifier := helpers.MakeSafeFilename(req.ResumableIdentifier, false)
	if safeIdentifier == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "invalid resumableIdentifier")
	}

	// Create a logger with more context for this specific upload operation.
	log := m.logger.WithFields(logrus.Fields{
		"roomId":              req.RoomId,
		"roomSid":             req.RoomSid,
		"userId":              req.UserId,
		"resumableIdentifier": safeIdentifier,
		"resumableFilename":   req.ResumableFilename,
		"method":              "ResumableFileUpload",
	})

	tempFolder := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid, config.UploadFileTempDir)
	chunkDir := filepath.Join(tempFolder, safeIdentifier)
	chunkPath := filepath.Join(chunkDir, fmt.Sprintf("part%d", req.ResumableChunkNumber))

	switch c.Method() {
	case fiber.MethodGet:
		{
			// If hook is enabled, we'll check with the script first.
			if m.app.Hooks != nil {
				hookData := &hooks.ResumableUploadHookData{
					Type:    hooks.ResumableUploadHookTypeCheck,
					RoomSid: req.RoomSid,
					RoomId:  req.RoomId,
					UserId:  req.UserId,

					ResumableIdentifier:       safeIdentifier,
					ResumableFilename:         req.ResumableFilename,
					ResumableChunkNumber:      req.ResumableChunkNumber,
					ResumableTotalChunks:      int32(req.ResumableTotalChunks),
					ResumableCurrentChunkSize: req.ResumableCurrentChunkSize,
					ResumableTotalSize:        req.ResumableTotalSize,
				}
				result, err := m.app.Hooks.RunResumableUploadHook(hookData, log)
				if err != nil {
					log.WithError(err).Error("resumable upload hook 'part-check' failed")
					return nil, fiber.NewError(fiber.StatusNoContent, "OK to upload")
				}
				if result != nil {
					if result.OutputResponseType == hooks.ResumableUploadOutputTypePartExists {
						res.Msg = "skipping upload as previously uploaded chunk"
						return res, fiber.NewError(fiber.StatusCreated, "skipping upload as previously uploaded chunk")
					}
					return nil, fiber.NewError(fiber.StatusNoContent, "OK to upload")
				}
			}
			// Original logic if no hook is configured.
			stat, err := os.Stat(chunkPath)
			if os.IsNotExist(err) {
				return res, fiber.NewError(fiber.StatusNoContent, "OK to upload")
			}
			if stat.Size() == req.ResumableCurrentChunkSize {
				res.Msg = "skipping upload as previously uploaded chunk"
				return res, fiber.NewError(fiber.StatusCreated, "skipping upload as previously uploaded chunk")
			}
			// Chunk is corrupted or size mismatch, remove it.
			_ = os.Remove(chunkPath)
			return nil, fiber.NewError(fiber.StatusNoContent, "OK to upload")
		}

	case fiber.MethodPost:
		{
			reqFile, err := c.FormFile("file")
			if err != nil {
				log.WithError(err).Errorln("failed to get 'file' from form-data")
				return nil, fiber.NewError(fiber.StatusBadRequest, "missing 'file' in form-data")
			}

			// for hook system we'll need to ensure dir exists
			if err := os.MkdirAll(chunkDir, 0755); err != nil {
				log.WithError(err).Errorln("failed to create chunk directory")
				return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to create chunk directory")
			}

			if req.ResumableChunkNumber == 1 {
				if req.ResumableTotalSize > int64(m.app.UploadFileSettings.MaxSize*1024*1024) {
					return nil, fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("file too large: max allowed is %dMB", m.app.UploadFileSettings.MaxSize))
				}

				file, err := reqFile.Open()
				if err != nil {
					log.WithError(err).Errorln("failed to open multipart file header")
					return nil, fiber.NewError(fiber.StatusInternalServerError, "failed to open uploaded file")
				}
				// detectMimeTypeForValidation will run f.Close() in defer
				if err := m.detectMimeTypeForValidation(file); err != nil {
					return nil, fiber.NewError(fiber.StatusUnsupportedMediaType, err.Error())
				}
			}

			// Always save the file to the local chunk path first.
			if err := c.SaveFile(reqFile, chunkPath); err != nil {
				log.WithError(err).Errorln("failed to write chunk data")
				return nil, fiber.NewError(fiber.StatusServiceUnavailable, "failed to write chunk data")
			}

			// If hook is enabled, pass the saved chunk to the script.
			if m.app.Hooks != nil {
				inputPath, _ := filepath.Abs(chunkPath)
				// The script is responsible for the chunk including remove the local copy.
				hookData := &hooks.ResumableUploadHookData{
					Type:      hooks.ResumableUploadHookTypeUpload,
					RoomSid:   req.RoomSid,
					RoomId:    req.RoomId,
					UserId:    req.UserId,
					InputPath: inputPath,

					ResumableIdentifier:       safeIdentifier,
					ResumableFilename:         req.ResumableFilename,
					ResumableChunkNumber:      req.ResumableChunkNumber,
					ResumableTotalChunks:      int32(req.ResumableTotalChunks),
					ResumableCurrentChunkSize: req.ResumableCurrentChunkSize,
					ResumableTotalSize:        req.ResumableTotalSize,
				}
				if _, err = m.app.Hooks.RunResumableUploadHook(hookData, log); err != nil {
					log.WithError(err).Error("resumable upload hook 'part-upload' failed")
					return nil, fiber.NewError(fiber.StatusServiceUnavailable, "hook failed to upload part")
				}
			}

			res.FilePath = "part_uploaded"
			return res, nil
		}
	}
	return res, nil
}

// UploadedFileMerge will combine all the parts and create a final file
func (m *FileModel) UploadedFileMerge(req *plugnmeet.UploadedFileMergeReq) (*plugnmeet.UploadedFileRes, error) {
	safeFilename := helpers.MakeSafeFilename(req.ResumableFilename, true)
	req.ResumableIdentifier = helpers.MakeSafeFilename(req.ResumableIdentifier, false) // should be fine here as already uploaded
	if req.ResumableIdentifier == "" {
		return nil, fmt.Errorf("invalid empty resumableIdentifier")
	}
	log := m.logger.WithFields(logrus.Fields{
		"roomId":              req.RoomId,
		"roomSid":             req.RoomSid,
		"resumableIdentifier": req.ResumableIdentifier,
		"resumableFilename":   req.ResumableFilename,
		"method":              "uploadedFileMergeHook",
	})

	var finalPath, fileMimeType, fileExtension string
	var err error

	// If hook is enabled, the script will perform the merge.
	if m.app.Hooks != nil {
		hookData := &hooks.ResumableUploadHookData{
			Type:                 hooks.ResumableUploadHookTypeMerge,
			RoomSid:              req.RoomSid,
			RoomId:               req.RoomId,
			FileType:             req.FileType.String(),
			ResumableIdentifier:  req.ResumableIdentifier,
			ResumableFilename:    safeFilename,
			ResumableTotalChunks: req.ResumableTotalChunks,
		}

		result, err := m.app.Hooks.RunResumableUploadHook(hookData, log)
		if err != nil {
			log.WithError(err).Error("resumable upload hook 'merge' failed")
			return nil, fmt.Errorf("resumable upload hook 'merge' failed")
		}
		if result != nil {
			if result.OutputResponseType != hooks.ResumableUploadOutputTypeMergeSuccess || result.OutputPath == "" {
				return nil, fmt.Errorf("resumable upload hook 'merge' did not return success status or output_path")
			}
			finalPath = result.OutputPath
			fileMimeType = result.FileMimeType
			fileExtension = result.FileExtension
		}
	} else {
		// default logic if no hook is configured.
		tempFolder := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid, config.UploadFileTempDir)
		chunkDir := filepath.Join(tempFolder, req.ResumableIdentifier)

		if _, err := os.Stat(chunkDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("requested file's chunks not found for identifier %s, make sure those were uploaded", req.ResumableIdentifier)
		}

		// combining chunks into one file
		combinedFile, err := m.combineResumableFiles(req, chunkDir, safeFilename)
		if err != nil {
			log.WithError(err).Error("failed to combine chunks")
			return nil, fmt.Errorf("failed to combine chunks")
		}

		// check the file size again
		stat, err := os.Stat(combinedFile)
		if err != nil {
			log.WithError(err).Error("failed to get file info")
			return nil, fmt.Errorf("failed to get file info")
		}
		if stat.Size() > int64(m.app.UploadFileSettings.MaxSize*1024*1024) {
			_ = os.Remove(combinedFile)
			return nil, fmt.Errorf("file too large: max allowed is %dMB", m.app.UploadFileSettings.MaxSize)
		}

		// we'll detect mime type again for sending data
		mType, err := mimetype.DetectFile(combinedFile)
		if err != nil {
			log.WithError(err).Error("mime detection failed")
			return nil, fmt.Errorf("mime detection failed")
		}

		if err := m.ValidateMimeType(mType); err != nil {
			_ = os.Remove(combinedFile)
			return nil, err
		}
		finalPath = filepath.Join(req.RoomSid, safeFilename)
		fileMimeType = mType.String()
		fileExtension = strings.Replace(mType.Extension(), ".", "", 1)
	}

	// Common logic for creating metadata and response
	fileId := uuid.NewString()
	if req.FileType != plugnmeet.RoomUploadedFileType_WHITEBOARD_CONVERTED_FILE {
		// we can save other files because this type file will process again
		// we'll save after complete conversion of that file
		meta := &plugnmeet.RoomUploadedFileMetadata{
			FileId:   fileId,
			FileName: safeFilename,
			FilePath: finalPath,
			FileType: req.FileType,
			MimeType: fileMimeType,
		}
		err = m.natsService.AddRoomFile(req.RoomId, meta)
		if err != nil {
			m.logger.WithFields(logrus.Fields{
				"roomId":   req.RoomId,
				"roomSid":  req.RoomSid,
				"filePath": finalPath,
			}).WithError(err).Error("failed to store file metadata in NATS")
		}
	}

	res := &plugnmeet.UploadedFileRes{
		Status:        true,
		Msg:           "file uploaded successfully",
		FileId:        fileId,
		FileType:      req.FileType,
		FileMimeType:  fileMimeType,
		FilePath:      finalPath,
		FileName:      safeFilename,
		FileExtension: fileExtension,
	}

	return res, nil
}

func (m *FileModel) combineResumableFiles(req *plugnmeet.UploadedFileMergeReq, chunksDir, safeFilename string) (string, error) {
	log := m.logger.WithFields(logrus.Fields{
		"roomId":              req.RoomId,
		"roomSid":             req.RoomSid,
		"resumableIdentifier": req.ResumableIdentifier,
		"resumableFilename":   req.ResumableFilename,
		"fileType":            req.FileType,
		"method":              "combineResumableFiles",
	})
	uploadDir := filepath.Join(m.app.UploadFileSettings.Path, req.RoomSid)

	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.WithError(err).Errorln("failed to create upload directory")
		return "", fmt.Errorf("failed to create upload directory: %w", err)
	}

	combinedFile := filepath.Join(uploadDir, safeFilename)
	destFile, err := os.Create(combinedFile)
	if err != nil {
		log.WithError(err).Errorln("failed to create combined file")
		return "", fmt.Errorf("failed to create combined file: %w", err)
	}
	defer destFile.Close()

	var i int32 = 1
	for i = 1; i <= req.ResumableTotalChunks; i++ {
		chunkPath := filepath.Join(chunksDir, fmt.Sprintf("part%d", i))
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			log.WithError(err).Errorf("failed to open chunk %d for merging", i)
			return "", fmt.Errorf("failed to open chunk %d: %w", i, err)
		}

		_, err = io.Copy(destFile, chunkFile)
		// Close inside the loop to free file descriptor early
		chunkFile.Close()
		if err != nil {
			log.WithError(err).Errorf("failed to write chunk %d to destination", i)
			return "", fmt.Errorf("failed to write chunk %d: %w", i, err)
		}
	}

	if err := os.RemoveAll(chunksDir); err != nil {
		log.WithError(err).Errorln("failed to remove chunk directory")
	}

	return combinedFile, nil
}

func (m *FileModel) UploadWhiteboardFileFromAuthApi(c fiber.Ctx, rf *plugnmeet.NatsKvRoomInfo) (statusCode plugnmeet.StatusCode, fiberErr *fiber.Error) {
	log := m.logger.WithFields(logrus.Fields{
		"method":  "UploadWhiteboardFileFromAuthApi",
		"roomId":  rf.RoomId,
		"roomSid": rf.RoomSid,
	})
	log.Info("New whiteboard file upload request received")

	// Lock to prevent concurrent processing in the same room
	lock := m.redisService.NewLock(fmt.Sprintf("whiteboardUploadLock-%s", rf.RoomId), 5*time.Minute)
	locked, err := lock.TryLock(c.RequestCtx())
	if err != nil {
		log.WithError(err).Error("failed to acquire lock")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, "failed to acquire lock")
	}
	if !locked {
		log.Warn("another whiteboard file upload is already in progress")
		return plugnmeet.StatusCode_CONFLICT, fiber.NewError(fiber.StatusConflict, "Another whiteboard file upload is already in progress for this room")
	}
	defer func() {
		if fiberErr != nil {
			// if error other than timeout then we'll unlock as it's real error
			_ = lock.Unlock(context.Background())
		}
	}()
	// otherwise unlock will be from the relevant process

	maxSize := m.app.UploadFileSettings.MaxSizeWhiteboardFile * 1024 * 1024
	documentLink := c.FormValue("document_link")
	if documentLink != "" {
		// fast checking and return if error
		if _, err := url.ParseRequestURI(documentLink); err != nil {
			return plugnmeet.StatusCode_INVALID_PARAMETERS, fiber.NewError(fiber.StatusBadRequest, "Invalid document_link provided")
		}

		gLog := m.logger.WithFields(logrus.Fields{
			"url": documentLink,
		})
		gLog.Info("Starting async download and processing of whiteboard file")
		errChan := make(chan error, 1)

		// Run download and conversion in the background
		go func() {
			_, err := m.DownloadAndProcessWhiteboardFile(rf.RoomId, rf.RoomSid, documentLink, maxSize, lock, gLog)
			errChan <- err
		}()

		select {
		case err := <-errChan:
			if err != nil {
				gLog.WithError(err).Error("Failed to process file")
				return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusBadRequest, err.Error())
			}
			gLog.Info("File successfully processed")
		case <-c.RequestCtx().Done():
			// The handler's timeout was reached.
			gLog.Warn("Handler timeout reached, conversion will continue in background")
		}

		return plugnmeet.StatusCode_SUCCESS, nil
	}

	// The rest of the function handles direct file uploads
	document, err := c.FormFile("document")
	if err != nil {
		return plugnmeet.StatusCode_INVALID_PARAMETERS, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// Check file size before saving
	if document.Size > int64(maxSize) {
		return plugnmeet.StatusCode_INVALID_PARAMETERS, fiber.ErrRequestEntityTooLarge
	}

	f, err := document.Open()
	if err != nil {
		return plugnmeet.StatusCode_INVALID_PARAMETERS, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// detectMimeTypeForValidation will run f.Close() in defer
	if err := m.detectMimeTypeForValidation(f); err != nil {
		return plugnmeet.StatusCode_INVALID_PARAMETERS, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	fileName := helpers.MakeSafeFilename(document.Filename, true)
	savePath := filepath.Join(rf.RoomSid, fileName)
	finalFile := filepath.Join(m.app.UploadFileSettings.Path, savePath)

	if err := os.MkdirAll(filepath.Join(m.app.UploadFileSettings.Path, rf.RoomSid), 0755); err != nil {
		log.WithError(err).Errorln("failed to create file directory")
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, "failed to create directory")
	}

	if err := c.SaveFile(document, finalFile); err != nil {
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	// now we're good to for start converting, ConvertAndBroadcastWhiteboardFile expect savePath not full path
	if _, err := m.ConvertAndBroadcastWhiteboardFile(c.RequestCtx(), rf.RoomId, rf.RoomSid, savePath, nil, lock, log); err != nil {
		if errors.Is(err, config.ErrConversionTimeout) {
			// process will continue in background
			return plugnmeet.StatusCode_SUCCESS, nil
		}
		return plugnmeet.StatusCode_INTERNAL_SERVER_ERROR, fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	log.Infof("File %s successfully uploaded and broadcasted", fileName)

	return plugnmeet.StatusCode_SUCCESS, nil
}

type WhiteboardPdfExportUploadReq struct {
	RoomSid     string `form:"roomSid"`
	RoomId      string `form:"roomId"`
	UserId      string `form:"userId"`
	FileId      string `form:"file_id"`
	FileName    string `form:"file_name"`
	PageNumber  int64  `form:"page_number"`
	SliceNumber int64  `form:"slice_number"`
	ExportId    string `form:"export_id"`
}

func (m *FileModel) WhiteboardPdfExportUpload(c fiber.Ctx, req *WhiteboardPdfExportUploadReq) *fiber.Error {
	log := m.logger.WithFields(logrus.Fields{
		"method":  "WhiteboardPdfExportUpload",
		"roomId":  req.RoomId,
		"roomSid": req.RoomSid,
	})

	// The rest of the function handles direct file uploads
	file, err := c.FormFile("file")
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	maxSize := m.app.UploadFileSettings.MaxSizeWhiteboardFile * 1024 * 1024
	// Check file size before saving
	if file.Size > int64(maxSize) {
		return fiber.ErrRequestEntityTooLarge
	}

	f, err := file.Open()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// detectMimeTypeForValidation will run f.Close() in defer
	if err := m.detectMimeTypeForValidation(f); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	fileName := helpers.MakeSafeFilename(file.Filename, true)
	savePath := m.buildWhiteboardPdfExportSavePath(req.RoomSid, req.ExportId)

	if err := os.MkdirAll(savePath, 0755); err != nil {
		log.WithError(err).Errorln("failed to create file directory")
		return fiber.NewError(fiber.StatusInternalServerError, "failed to create directory")
	}

	if err := c.SaveFile(file, path.Join(savePath, fileName)); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return nil
}

func (m *FileModel) buildWhiteboardPdfExportSavePath(roomSid, exportId string) string {
	return filepath.Join(m.app.UploadFileSettings.Path, roomSid, "tmp", exportId)
}
