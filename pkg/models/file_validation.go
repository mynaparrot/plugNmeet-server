package models

import (
	"fmt"
	"mime/multipart"
	"sort"
	"strings"

	"github.com/gabriel-vasile/mimetype"
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
		return fmt.Errorf("invalid file")
	}

	for _, t := range allowedTypes {
		if ext == t {
			return nil
		}
	}

	return fmt.Errorf(mtype.Extension() + " file type not allowed")
}
