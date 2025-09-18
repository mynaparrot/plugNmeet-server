package models

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mynaparrot/plugnmeet-protocol/plugnmeet"
)

func (m *AnalyticsModel) DeleteAnalytics(r *plugnmeet.DeleteAnalyticsReq) error {
	analytic, err := m.fetchAnalytic(r.FileId)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("%s/%s", *m.app.AnalyticsSettings.FilesStorePath, analytic.FileName)

	// delete the main file
	err = os.Remove(path)
	if err != nil {
		// if file not exists then we can delete it from record without showing any error
		if !os.IsNotExist(err) {
			ms := strings.SplitN(err.Error(), "/", -1)
			return errors.New(ms[len(ms)-1])
		}
	}

	// delete compressed, if any
	_ = os.Remove(path + ".fiber.gz")

	// no error, so we'll delete record from DB
	_, err = m.ds.DeleteAnalyticByFileId(analytic.FileId)
	if err != nil {
		return err
	}
	return nil
}
