package models

import (
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
)

func (m *FileModel) DeleteRoomUploadedDir(roomSid string) error {
	if roomSid == "" {
		return errors.New("empty sid")
	}
	path := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomSid)
	err := os.RemoveAll(path)
	if err != nil {
		log.Errorln(err)
	}
	return err
}
