package controllers

import "github.com/mynaparrot/plugNmeet/internal/models"

func StartScheduler() {
	m := models.NewScheduler()
	m.StartScheduler()
}
