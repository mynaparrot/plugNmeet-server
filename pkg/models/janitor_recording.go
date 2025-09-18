package models

import (
	"os"
	"path"
	"time"

	"github.com/sirupsen/logrus"
)

func (m *JanitorModel) checkDelRecordingBackupPath() {
	if !m.app.RecorderInfo.EnableDelRecordingBackup {
		// nothing to do
		return
	}
	log := m.logger.WithField("task", "checkDelRecordingBackupPath")

	locked := m.rs.IsJanitorTaskLock("checkDelRecordingBackupPath")
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	m.rs.LockJanitorTask("checkDelRecordingBackupPath", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockJanitorTask("checkDelRecordingBackupPath")

	checkTime := time.Now().Add(-m.app.RecorderInfo.DelRecordingBackupDuration)
	entries, err := os.ReadDir(m.app.RecorderInfo.DelRecordingBackupPath)
	if err != nil {
		log.WithError(err).Errorln("failed to read recording backup directory")
		return
	}
	for _, et := range entries {
		if et.IsDir() {
			continue
		}
		info, err := et.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(checkTime) {
			// we can remove this file
			fileToDelete := path.Join(m.app.RecorderInfo.DelRecordingBackupPath, et.Name())
			log.WithFields(logrus.Fields{
				"file":         fileToDelete,
				"modTime":      info.ModTime().Format(time.RFC3339),
				"ageThreshold": checkTime.Format(time.RFC3339),
				"backupMaxAge": m.app.RecorderInfo.DelRecordingBackupDuration.String(),
			}).Warn("deleting expired recording backup file")
			// video file
			err = os.Remove(fileToDelete)
			if err != nil {
				m.logger.WithError(err).Errorln("error deleting file")
			}
			// info JSON file
			err = os.Remove(fileToDelete + ".json")
			if err != nil {
				m.logger.WithError(err).Errorln("error deleting file")
			}
		}
	}
}
