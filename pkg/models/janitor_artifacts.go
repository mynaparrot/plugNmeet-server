package models

import (
	"os"
	"path"
	"time"

	"github.com/sirupsen/logrus"
)

func (m *JanitorModel) checkDelArtifactsBackupPath() {
	if !m.app.ArtifactsSettings.EnableDelArtifactsBackup {
		// nothing to do
		return
	}
	log := m.logger.WithField("task", "checkDelArtifactsBackupPath")

	// Calculate the time threshold using UTC for consistency.
	checkTime := time.Now().UTC().Add(-m.app.ArtifactsSettings.DelArtifactsBackupDuration)

	entries, err := os.ReadDir(m.app.ArtifactsSettings.DelArtifactsBackupPath)
	if err != nil {
		log.WithError(err).Errorln("failed to read artifacts backup directory")
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// info.ModTime() is also in UTC, so this is a direct, consistent comparison.
		if info.ModTime().Before(checkTime) {
			fileToDelete := path.Join(m.app.ArtifactsSettings.DelArtifactsBackupPath, entry.Name())
			log.WithFields(logrus.Fields{
				"file":         fileToDelete,
				"modTime":      info.ModTime().Format(time.RFC3339),
				"ageThreshold": checkTime.Format(time.RFC3339),
				"backupMaxAge": m.app.ArtifactsSettings.DelArtifactsBackupDuration.String(),
			}).Warn("deleting expired artifact backup file")

			err = os.Remove(fileToDelete)
			if err != nil {
				m.logger.WithError(err).Errorln("error deleting artifact backup file")
			}
		}
	}
}
