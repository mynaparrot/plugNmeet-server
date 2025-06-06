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

	ext := strings.TrimPrefix(mtype.Extension(), ".")
	if ext == "" {
		return errors.New("invalid file")
	}

	for _, t := range allowedTypes {
		if ext == t {
			return nil
		}
	}

	return errors.New(mtype.Extension() + " file type not allowed")
}
