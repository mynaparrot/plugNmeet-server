package models

import (
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"time"
)

func (m *SchedulerModel) checkDelRecordingBackupPath() {
	if !m.app.RecorderInfo.EnableDelRecordingBackup {
		// nothing to do
		return
	}

	locked := m.rs.IsSchedulerTaskLock("checkDelRecordingBackupPath")
	if locked {
		// if lock then we will not perform here
		return
	}

	// now set lock
	_ = m.rs.LockSchedulerTask("checkDelRecordingBackupPath", time.Minute*1)
	// clean at the end
	defer m.rs.UnlockSchedulerTask("checkDelRecordingBackupPath")

	checkTime := time.Now().Add(-m.app.RecorderInfo.DelRecordingBackupDuration)
	entries, err := os.ReadDir(m.app.RecorderInfo.DelRecordingBackupPath)
	if err != nil {
		log.Errorln(err)
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
			log.Infoln("deleting file:", fileToDelete, "because of created", checkTime, "which is older than", m.app.RecorderInfo.DelRecordingBackupDuration)
			// video file
			err = os.Remove(fileToDelete)
			if err != nil {
				log.Errorln(err)
			}
			// info JSON file
			err = os.Remove(fileToDelete + ".json")
			if err != nil {
				log.Errorln(err)
			}
		}
	}
}
