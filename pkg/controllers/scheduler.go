package controllers

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/models/schedulermodel"
)

func StartScheduler() {
	m := schedulermodel.New(nil, nil, nil, nil)
	m.StartScheduler()
}
