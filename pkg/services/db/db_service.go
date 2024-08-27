package dbservice

import (
	"gorm.io/gorm"
)

type DatabaseService struct {
	db *gorm.DB
}

func New(db *gorm.DB) *DatabaseService {
	return &DatabaseService{
		db: db,
	}
}
