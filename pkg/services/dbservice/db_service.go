package dbservice

import (
	"gorm.io/gorm"
)

type DatabaseService struct {
	db *gorm.DB
}

func NewDBService(db *gorm.DB) *DatabaseService {
	return &DatabaseService{
		db: db,
	}
}
