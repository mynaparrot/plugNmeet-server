package models

import (
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
)

func (m *FileModel) DeleteRoomUploadedDir() error {
	if m.req == nil || m.req.Sid == "" {
		return errors.New("empty sid")
	}
	path := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, m.req.Sid)
	err := os.RemoveAll(path)
	if err != nil {
		log.Errorln(err)
	}
	return err
}
