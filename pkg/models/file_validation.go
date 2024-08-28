package models

import (
	"errors"
	"github.com/gabriel-vasile/mimetype"
	"github.com/gofiber/fiber/v2"
	"mime/multipart"
	"sort"
	"strings"
)

func (m *FileModel) detectMimeTypeForValidation(file multipart.File) error {
	defer file.Close()
	mtype, err := mimetype.DetectReader(file)
	if err != nil {
		return err
	}
	return m.ValidateMimeType(mtype)
}

func (m *FileModel) ValidateMimeType(mtype *mimetype.MIME) error {
	allowedTypes := m.app.UploadFileSettings.AllowedTypes
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

func (m *FileModel) CommonValidation(c *fiber.Ctx) error {
	roomId := c.Locals("roomId")
	requestedUserId := c.Locals("requestedUserId")

	if roomId == "" {
		return errors.New("no roomId in token")
	}
	if roomId != m.req.RoomId {
		return errors.New("token roomId & requested roomId didn't matched")
	}
	if requestedUserId != m.req.UserId {
		return errors.New("token UserId & requested UserId didn't matched")
	}

	return nil
}
