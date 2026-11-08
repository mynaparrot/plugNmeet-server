package controllers

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/gofiber/fiber/v3"
	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/sirupsen/logrus"
)

// sendFile is indirected so the file serving failure path can be exercised by
// tests; production always uses Fiber's SendFile.
var sendFile = func(c fiber.Ctx, path string) error {
	return c.SendFile(path)
}

// downloadTokenErrorResponse answers a failed download token verification. The
// verified status code is kept, but its error text stays server-side so no JWT or
// filesystem detail reaches the browser; anything unexpected becomes a 500.
// A verifier may hand back the sanitized relative input path of the token, which
// only names the file by its basename.
func downloadTokenErrorResponse(c fiber.Ctx, res *hooks.DownloadHookData, status int, err error, log *logrus.Entry) error {
	log.WithError(err).WithField("status", status).Warn("download token verification failed")

	switch status {
	case fiber.StatusUnauthorized:
		return c.Status(status).SendString(config.MsgFileDownloadTokenInvalid)
	case fiber.StatusBadRequest:
		return c.Status(status).SendString(config.MsgFileDownloadInvalidPath)
	case fiber.StatusNotFound:
		var inputPath string
		if res != nil {
			inputPath = res.InputPath
		}
		return c.Status(status).SendString(config.FileDownloadNotFoundMsg(inputPath))
	default:
		return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
	}
}

// serveDownloadHookResult turns a successful verification into the browser
// response: a redirect, or the verified local file.
func serveDownloadHookResult(c fiber.Ctx, res *hooks.DownloadHookData, log *logrus.Entry) error {
	if res == nil {
		log.Error("download verification returned no result")
		return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
	}

	switch res.Action {
	case hooks.DownloadHookDataActionRedirect:
		if res.RedirectUrl == "" {
			log.Error("download hook did not return a redirect url")
			return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
		}
		return c.Redirect().Status(fiber.StatusTemporaryRedirect).To(res.RedirectUrl)
	case hooks.DownloadHookDataActionServeLocal:
		if res.OutputPath == "" || res.MimeType == "" {
			log.WithFields(logrus.Fields{
				"hasOutputPath": res.OutputPath != "",
				"hasMimeType":   res.MimeType != "",
			}).Error("download hook returned an incomplete serve_local result")
			return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
		}
		c.Set(fiber.HeaderContentType, res.MimeType)
		c.Set(fiber.HeaderContentDisposition, "attachment; filename="+filepath.Base(res.OutputPath))
		return serveLocalFile(c, res.OutputPath, log)
	default:
		log.WithField("action", res.Action).Error("download hook returned an unsupported action")
		return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
	}
}

// serveLocalFile sends the verified file and keeps its path out of the response.
func serveLocalFile(c fiber.Ctx, path string, log *logrus.Entry) error {
	if err := sendFile(c, path); err != nil {
		// the path is operational detail: it belongs in the log, never in the body
		log.WithError(err).WithField("path", path).Error("failed to send the download file")
		if errors.Is(err, fs.ErrNotExist) {
			return c.Status(fiber.StatusNotFound).SendString(config.FileDownloadNotFoundMsg(path))
		}
		return c.Status(fiber.StatusInternalServerError).SendString(config.MsgFileDownloadFailed)
	}
	return nil
}
