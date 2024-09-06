package models

import (
	"errors"
	"github.com/gabriel-vasile/mimetype"
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
