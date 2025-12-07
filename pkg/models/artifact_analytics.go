package models

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/dbmodels"
	"github.com/sirupsen/logrus"
	"google.golang.org/protobuf/encoding/protojson"
)

// CreateAnalyticsArtifact creates a new artifact record for a generated analytics file.
// It saves the provided JSON data to a file in the artifact storage path and records the metadata in the database.
func (m *ArtifactModel) CreateAnalyticsArtifact(roomTableId uint64, jsonData []byte, log *logrus.Entry) (*dbmodels.RoomArtifact, error) {
	log = log.WithFields(logrus.Fields{
		"method":      "CreateAnalyticsArtifact",
		"roomTableId": roomTableId,
	})

	// Get room info
	roomInfo, err := m.ds.GetRoomInfoByTableId(roomTableId)
	if err != nil {
		return nil, fmt.Errorf("failed to get room info for room %d: %w", roomTableId, err)
	}
	if roomInfo == nil {
		return nil, fmt.Errorf("room not found for room %d", roomTableId)
	}

	// Generate a unique filename for the analytics JSON file.
	fileName := fmt.Sprintf("analytics-%s-%d.json", roomInfo.Sid, time.Now().Unix())

	relativePath, absolutePath, err := m.buildPath(fileName, roomInfo.RoomId, plugnmeet.RoomArtifactType_MEETING_ANALYTICS)
	if err != nil {
		return nil, fmt.Errorf("failed to build artifact path: %w", err)
	}

	// Write the JSON data to the file
	err = os.WriteFile(absolutePath, jsonData, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write analytics file: %w", err)
	}
	log.Infof("wrote analytics file to %s", absolutePath)

	metadata := &plugnmeet.RoomArtifactMetadata{
		FileInfo: &plugnmeet.RoomArtifactFileInfo{
			FilePath: relativePath,
			FileSize: int64(len(jsonData)),
			MimeType: "application/json",
		},
	}

	artifact, err := m.createAndSaveArtifact(roomInfo.RoomId, roomInfo.Sid, roomTableId, plugnmeet.RoomArtifactType_MEETING_ANALYTICS, metadata, log)
	if err != nil {
		// If creating the artifact fails, we should try to remove the file we just created.
		log.WithError(err).Error("failed to create analytics artifact in database")
		if errRemove := os.Remove(absolutePath); errRemove != nil {
			log.WithError(errRemove).Errorf("CRITICAL: failed to remove analytics file at %s after DB error. The file exists without a DB record.", absolutePath)
		}
		return nil, err
	}

	return artifact, nil
}

//-- Migration logic here--
// TODO: will remove in future

// copyFile performs a copy of a file from a source to a destination.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	return destFile.Sync()
}

// MigrateAnalyticsToArtifacts performs a one-time migration of old analytics files
// to the new room artifacts structure.
func (m *ArtifactModel) MigrateAnalyticsToArtifacts() {
	log := m.log.WithField("method", "MigrateAnalyticsToArtifacts")
	log.Info("starting analytics to artifacts migration...")

	oldAnalytics, err := m.ds.GetAllAnalyticsFiles()
	if err != nil {
		log.WithError(err).Error("failed to get old analytics files from db")
		return
	}

	if len(oldAnalytics) == 0 {
		log.Info("no old analytics files found to migrate")
		return
	}

	log.Infof("found %d old analytics records to process", len(oldAnalytics))
	var migratedCount, skippedCount int

	for _, analytic := range oldAnalytics {
		oldPath := filepath.Join(*m.app.AnalyticsSettings.FilesStorePath, analytic.FileName)
		oldStat, err := os.Stat(oldPath)
		if os.IsNotExist(err) {
			log.Warnf("source analytics file not found, skipping: %s", oldPath)
			skippedCount++
			continue
		}

		roomInfo, err := m.ds.GetRoomInfoByTableId(analytic.RoomTableID)
		if err != nil || roomInfo == nil {
			log.WithError(err).Errorf("failed to get room info for room_table_id %d, skipping", analytic.RoomTableID)
			continue
		}

		relativePath, absolutePath, err := m.buildPath(analytic.FileName, roomInfo.RoomId, plugnmeet.RoomArtifactType_MEETING_ANALYTICS)
		if err != nil {
			log.WithError(err).Errorf("failed to build new artifact path for file %s", analytic.FileName)
			continue
		}

		// Use copy then delete for robustness across filesystems.
		err = copyFile(oldPath, absolutePath)
		if err != nil {
			log.WithError(err).Errorf("failed to copy file from %s to %s", oldPath, absolutePath)
			continue
		}

		metadataBytes, err := protojson.Marshal(&plugnmeet.RoomArtifactMetadata{
			FileInfo: &plugnmeet.RoomArtifactFileInfo{
				FilePath: relativePath,
				FileSize: oldStat.Size(),
				MimeType: "application/json",
			},
		})
		if err != nil {
			log.WithError(err).Error("failed to marshal metadata")
			continue
		}

		artifact := &dbmodels.RoomArtifact{
			ArtifactId:  analytic.FileID, // Use old file_id as the new unique artifact_id
			RoomTableID: analytic.RoomTableID,
			RoomId:      roomInfo.RoomId,
			Type:        dbmodels.RoomArtifactType(plugnmeet.RoomArtifactType_MEETING_ANALYTICS),
			Metadata:    string(metadataBytes),
		}

		// Use the existing DB method, which doesn't trigger webhooks.
		_, err = m.ds.CreateRoomArtifact(artifact)
		if err != nil {
			// Clean up the copied file if DB insert fails.
			_ = os.Remove(absolutePath)

			var mysqlErr *mysql.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 { // 1062 is the error number for duplicate entry
				log.Warnf("artifact for file_id %s already exists, skipping.", analytic.FileID)
				skippedCount++
			} else {
				log.WithError(err).Errorf("failed to create artifact record for file %s", analytic.FileName)
			}
			continue
		}

		// If DB insert is successful, delete the original file.
		_ = os.Remove(oldPath)
		migratedCount++
	}

	log.Infof("analytics migration finished. Migrated: %d, Skipped: %d", migratedCount, skippedCount)
}
