package models

import (
	"fmt"
	"os"
)

func (m *FileModel) DeleteRoomUploadedDir(roomSid string) error {
	if roomSid == "" {
		return fmt.Errorf("empty sid")
	}
	path := fmt.Sprintf("%s/%s", m.app.UploadFileSettings.Path, roomSid)
	err := os.RemoveAll(path)
	if err != nil {
		m.logger.Errorln(err)
	}
	return err
}
