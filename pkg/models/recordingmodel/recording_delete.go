package recordingmodel

import (
	"errors"
	"fmt"
	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	log "github.com/sirupsen/logrus"
	"io"
	"os"
	"strings"
)

func (a *AuthRecording) DeleteRecording(r *plugnmeet.DeleteRecordingReq) error {
	recording, err := a.FetchRecording(r.RecordId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", config.GetConfig().RecorderInfo.RecordingFilesPath, recording.FilePath)
	fileExist := true

	f, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, err.(*os.PathError)) {
			log.Errorln(recording.FilePath + " does not exist, so deleting from DB without stopping")
			fileExist = false
		} else {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// if file not exists then will delete
	// if not, we can just skip this & delete from DB
	if fileExist {
		err = os.Remove(path)
		if err != nil {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// delete compressed, if any
	_ = os.Remove(path + ".fiber.gz")
	// delete record info file too
	_ = os.Remove(path + ".json")

	// we will check if the directory is empty or not
	// if empty then better to delete that directory
	if fileExist {
		dir := strings.Replace(path, f.Name(), "", 1)
		if dir != config.GetConfig().RecorderInfo.RecordingFilesPath {
			empty, err := a.isDirEmpty(dir)
			if err == nil && empty {
				err = os.Remove(dir)
				if err != nil {
					log.Error(err)
				}
			}
		}
	}

	// no error, so we'll delete record from DB
	_, err = a.ds.DeleteRecording(r.RecordId)
	if err != nil {
		return err
	}
	return nil
}

func (a *AuthRecording) isDirEmpty(name string) (bool, error) {
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
