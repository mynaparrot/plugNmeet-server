package controllers

import "github.com/mynaparrot/plugnmeet-server/pkg/models"

// SchedulerController holds dependencies for scheduler-related tasks.
type SchedulerController struct {
	SchedulerModel *models.SchedulerModel
}

// NewSchedulerController creates a new SchedulerController.
func NewSchedulerController(m *models.SchedulerModel) *SchedulerController {
	return &SchedulerController{
		SchedulerModel: m,
	}
}

// StartScheduler starts the scheduler.
func (sc *SchedulerController) StartScheduler() {
	sc.SchedulerModel.StartScheduler()
}
