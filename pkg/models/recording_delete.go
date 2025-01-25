package models

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"
)

func (m *RecordingModel) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	recording, err := m.FetchRecording(r.RecordId)
	if err != nil {
		return err
	}

	filePath := fmt.Sprintf("%s/%s", config.GetConfig().RecorderInfo.RecordingFilesPath, recording.FilePath)
	fileExist := true

	f, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, err.(*os.PathError)) {
			log.Errorln(filePath + " does not exist, so deleting from DB without stopping")
			fileExist = false
		} else {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// if file not exists then will delete
	// if not, we can just skip this & delete from DB
	if fileExist {
		// if enabled backup
		if m.app.RecorderInfo.EnableDelRecordingBackup {
			// first with the video file
			toFile := path.Join(m.app.RecorderInfo.DelRecordingBackupPath, f.Name())
			err := os.Rename(filePath, toFile)
			if err != nil {
				log.Errorln(err)
				return err
			}

			// touch to change modified date
			// otherwise during cleanup will be hard to detect
			cmd := exec.Command("/usr/bin/touch", "-d", "2 minutes ago", toFile)
			output, err := cmd.CombinedOutput()
			if err != nil {
				log.Errorln(fmt.Sprintf("Failed change modify time of the dir: %s with error: %s", toFile, string(output)))
				return err
			}

			// now the JSON file
			err = os.Rename(filePath+".json", toFile+".json")
			if err != nil {
				// just log
				log.Errorln(err)
			}

		} else {
			err = os.Remove(filePath)
			if err != nil {
				ms := strings.SplitN(err.Error(), "/", -1)
				return errors.New(ms[len(ms)-1])
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
		if dir != config.GetConfig().RecorderInfo.RecordingFilesPath {
			empty, err := m.isDirEmpty(dir)
			if err == nil && empty {
				err = os.Remove(dir)
				if err != nil {
					log.Error(err)
				}
			}
		}
	}

	// no error, so we'll delete record from DB
	_, err = m.ds.DeleteRecording(r.RecordId)
	if err != nil {
		return err
	}
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
