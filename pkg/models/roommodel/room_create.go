package roommodel

import (
	"github.com/mynaparrot/plugnmeet-server/pkg/config"
	"github.com/mynaparrot/plugnmeet-server/pkg/services/dbservice"
)

type RoomCreateModel struct {
	ds *dbservice.DatabaseService
}

func NewRoomCreateModel() *RoomCreateModel {
	return &RoomCreateModel{
		ds: dbservice.NewDBService(config.GetConfig().ORM),
	}
}

func (m *RoomCreateModel) CreateRoom() {

}
