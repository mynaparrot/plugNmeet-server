package dbservice

import (
	"fmt"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/helpers"
	"path/filepath"
	"runtime"
	"time"
)

var (
	_, b, _, _ = runtime.Caller(0)
	root       = filepath.Join(filepath.Dir(b), "../../..")
)

var s *DatabaseService
var sid = fmt.Sprintf("%d", time.Now().Unix())
var roomTableId uint64
var roomId = "test01"

func init() {
	err := helpers.PrepareServer(root + "/config.yaml")
	if err != nil {
		panic(err)
	}
	s = NewDBService(config.AppCnf.ORM)
}
