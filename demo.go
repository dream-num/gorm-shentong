package main

import (
	"fmt"
	"strings"

	"github.com/Mystery00/gorm-shentong/oscar"
	"github.com/Mystery00/gorm-shentong/shentong"
	"gorm.io/gorm"
)

func main() {
	db, err := gorm.Open(shentong.New(shentong.Config{
		DSN: "user/password@host:port/dbname",
		DSNConfig: &oscar.Config{
			User:   "test",
			Passwd: "testPasswd",
			Host:   "127.0.0.1",
			Port:   2003,
			DBName: "OSRDB",
		},
		FieldConvertType: shentong.Custom,
		FieldConvertFunc: func(s string) string {
			return strings.ToUpper(s)
		},
	}))

	if err != nil {
		panic(err)
	}
	fmt.Printf("连接成功：%s", db.Dialector.Name())
}
