package schedulercontroller

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/models/scheduler"
)

func StartScheduler() {
	m := schedulermodel.New(nil, nil, nil, nil)
	m.StartScheduler()
}
