package database

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var GlobalDB *gorm.DB

func InitDB() error {
	db, err := gorm.Open(sqlite.Open("lyrical.db"), &gorm.Config{})
	if err != nil {
		return err
	}

	if err := db.AutoMigrate(&Song{}); err != nil {
		return err
	}

	GlobalDB = db
	return nil
}
