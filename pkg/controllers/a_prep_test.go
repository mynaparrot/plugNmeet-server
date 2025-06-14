package controllers

import (
	"github.com/google/uuid"
	"github.com/mynaparrot/plugnmeet-server/helpers"
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"path/filepath"
	"runtime"
)

var (
	_, b, _, _ = runtime.Caller(0)
	root       = filepath.Join(filepath.Dir(b), "../..")
)

var roomId = uuid.NewString()
var userId = uuid.NewString()

func init() {
	appCnf, err := helpers.ReadYamlConfigFile(root + "/config.yaml")
	if err != nil {
		panic(err)
	}

	appCnf.RootWorkingDir = root
	// set this config for global usage
	config.New(appCnf)

	// now prepare server
	err = helpers.PrepareServer(config.GetConfig())
	if err != nil {
		panic(err)
	}
}
