package controllers

import "github.com/mynaparrot/plugNmeet/pkg/models"

func StartScheduler() {
	m := models.NewSchedulerModel()
	m.StartScheduler()
}
