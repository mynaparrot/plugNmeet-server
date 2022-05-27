package factory

import (
	"database/sql"
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/mynaparrot/plugNmeet/internal/config"
	log "github.com/sirupsen/logrus"
	"time"
)

var DB *sql.DB

func NewDbConnection() {
	db, err := sql.Open("mysql", fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", config.AppCnf.MySqlInfo.Username, config.AppCnf.MySqlInfo.Password, config.AppCnf.MySqlInfo.Host, config.AppCnf.MySqlInfo.Port, config.AppCnf.MySqlInfo.DBName))

	if err != nil {
		log.Panicln(err)
	}

	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(150)
	db.SetMaxIdleConns(5)

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	config.AppCnf.DB = db
}

func SetDBConnection(d *sql.DB) {
	err := d.Ping()
	if err != nil {
		panic(err)
	}

	DB = d
}
