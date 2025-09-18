package models

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/sirupsen/logrus"
)

func (m *RecordingModel) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	log := m.logger.WithFields(logrus.Fields{
		"recordId": r.RecordId,
		"method":   "DeleteRecording",
	})
	log.Infoln("request to delete recording")

	recording, err := m.FetchRecording(r.RecordId)
	if err != nil {
		log.WithError(err).Errorln("failed to fetch recording info")
		return err
	}

	filePath := fmt.Sprintf("%s/%s", m.app.RecorderInfo.RecordingFilesPath, recording.FilePath)
	log = log.WithField("filePath", filePath)
	fileExist := true

	f, err := os.Stat(filePath)
	if err != nil {
		var pathError *fs.PathError
		if errors.As(err, &pathError) {
			log.WithError(err).Warnln("recording file does not exist, will proceed to delete DB record")
			fileExist = false
		} else {
			ms := strings.SplitN(err.Error(), "/", -1)
			err = fmt.Errorf(ms[len(ms)-1])
			log.WithError(err).Errorln("failed to stat recording file")
			return err
		}
	}

	// if the file not exists then will delete
	// if not, we can just skip this and delete from DB
	if fileExist {
		// if enabled backup
		if m.app.RecorderInfo.EnableDelRecordingBackup {
			log.Info("backing up recording before deletion")
			// first with the video file
			toFile := path.Join(m.app.RecorderInfo.DelRecordingBackupPath, f.Name())
			err := os.Rename(filePath, toFile)
			if err != nil {
				log.WithError(err).Errorln("error moving file to backup")
				return err
			}

			// otherwise during cleanup will be hard to detect
			newTime := time.Now()
			if err := os.Chtimes(toFile, newTime, newTime); err != nil {
				log.WithError(err).Warnln("failed to update file modification time for backup")
			}

			// now the JSON file
			err = os.Rename(filePath+".json", toFile+".json")
			if err != nil {
				// just log
				log.WithError(err).Warnln("error moving json info file to backup")
			}

		} else {
			log.Info("deleting recording file permanently")
			err = os.Remove(filePath)
			if err != nil {
				ms := strings.SplitN(err.Error(), "/", -1)
				err = fmt.Errorf(ms[len(ms)-1])
				log.WithError(err).Errorln("failed to remove recording file")
				return err
			}
		}
	}

	// delete compressed, if any
	_ = os.Remove(filePath + ".fiber.gz")
	// delete record info file too
	_ = os.Remove(filePath + ".json")

	// we will check if the directory is empty or not
	// if empty then better to delete that directory
	if fileExist {
		dir := strings.Replace(filePath, f.Name(), "", 1)
		if dir != m.app.RecorderInfo.RecordingFilesPath {
			log.WithField("dir", dir).Info("checking if recording directory is empty")
			empty, err := m.isDirEmpty(dir)
			if err == nil && empty {
				log.Info("recording directory is empty, removing it")
				err = os.Remove(dir)
				if err != nil {
					log.WithError(err).Error("error deleting directory")
				}
			}
		}
	}

	// no error, so we'll delete record from DB
	log.Info("deleting recording record from database")
	_, err = m.ds.DeleteRecording(r.RecordId)
	if err != nil {
		log.WithError(err).Errorln("failed to delete recording from db")
		return err
	}

	log.Info("successfully deleted recording")
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
