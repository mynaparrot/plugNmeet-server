package controllers

import "github.com/mynaparrot/plugnmeet-server/pkg/models"

func StartScheduler() {
	m := models.NewSchedulerModel()
	m.StartScheduler()
}
