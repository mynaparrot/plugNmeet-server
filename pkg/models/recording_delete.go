package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/hooks"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RecordingModel) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"recordId": r.RecordId,
		"method":   "DeleteRecording",
	})
	log.Infoln("Request to delete recording received")

	recording, err := m.FetchRecording(r.RecordId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch recording info")
		return err
	}

	// If delete hook is configured, we'll use it.
	if m.app.Hooks != nil && m.app.HookManager != nil && m.app.Hooks.DeleteHook != nil && len(m.app.Hooks.DeleteHook.Scripts) > 0 {
		delReq := hooks.DeleteHookData{
			InputPath:    recording.FilePath,
			HookFileType: hooks.HookFileTypeRecording,
		}
		resBytes, err := hooks.ExecuteHookPipeline(m.app.HookManager, m.app.Hooks.DeleteHook.Scripts, &delReq, m.app.Hooks.DeleteHook.HookTimeout, log)
		if err != nil {
			log.WithError(err).Warn("delete hook pipeline failed for recording")
		} else {
			var res hooks.DeleteHookData
			if err := json.Unmarshal(resBytes, &res); err != nil {
				log.WithError(err).Warn("failed to unmarshal delete hook response")
			} else if res.Error != "" {
				log.Warnf("delete hook script returned an error: %s", res.Error)
			}
		}
		// After running the hook (even if it failed), we proceed to delete the DB record.
		// The hook is fire-and-forget; its failure should not block DB cleanup.
	} else {
		// Otherwise, if it's a local file, we'll try to delete it.
		filePath := fmt.Sprintf("%s/%s", m.app.RecorderInfo.RecordingFilesPath, recording.FilePath)
		log.WithField("filePath", filePath).Info("deleting local recording file")
		fileExist := true

		f, err := os.Stat(filePath)
		if err != nil {
			if _, ok := errors.AsType[*fs.PathError](err); ok {
				log.WithError(err).Warn("recording file does not exist, will proceed to delete DB record")
				fileExist = false
			}
		}

		if fileExist {
			if m.app.RecorderInfo.EnableDelRecordingBackup {
				log.Info("backing up recording before deletion")
				toFile := path.Join(m.app.RecorderInfo.DelRecordingBackupPath, f.Name())
				if err := os.Rename(filePath, toFile); err != nil {
					log.WithError(err).Errorln("error moving file to backup")
					return err
				}
				newTime := time.Now().UTC()
				if err := os.Chtimes(toFile, newTime, newTime); err != nil {
					log.WithError(err).Warnln("failed to update file modification time for backup")
				}
				metadataFile := toFile + ".json"
				if err = os.Rename(filePath+".json", metadataFile); err == nil {
					if err := os.Chtimes(metadataFile, newTime, newTime); err != nil {
						log.WithError(err).Warnln("failed to update file modification time for backup")
					}
				}
			} else {
				log.Info("deleting recording file permanently")
				if err = os.Remove(filePath); err != nil {
					log.WithError(err).Errorln("failed to remove recording file")
					return fmt.Errorf("delete recording file permanently failed")
				}
			}
		}

		_ = os.Remove(filePath + ".fiber.gz")
		_ = os.Remove(filePath + ".json")

		if fileExist {
			dir := strings.Replace(filePath, f.Name(), "", 1)
			if dir != m.app.RecorderInfo.RecordingFilesPath {
				log.WithField("dir", dir).Info("checking if recording directory is empty")
				empty, err := m.isDirEmpty(dir)
				if err == nil && empty {
					log.Info("recording directory is empty, removing it")
					if err := os.Remove(dir); err != nil {
						log.WithError(err).Error("error deleting directory")
					}
				}
			}
		}
	}

	// no error, so we'll delete record from DB
	log.Info("deleting recording record from database")
	if _, err := m.ds.DeleteRecording(r.RecordId); err != nil {
		log.WithError(err).Errorln("failed to delete recording from db")
		return err
	}

	log.Info("Successfully deleted recording")
	return nil
}

func (m *RecordingModel) isDirEmpty(name string) (bool, error) {
	f, err := os.Open(name)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.Readdirnames(1) // Or f.Readdir(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err // Either not empty or error, suits both cases
}
