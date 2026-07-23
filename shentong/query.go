package shentong

import (
	"strings"

	"gorm.io/gorm"
)

type fieldConverter func(string) string

func queryFix(db *gorm.DB) {
	config := db.Dialector.(*Dialector).Config
	if config.FieldConvertType == None {
		//不转换，直接返回
		return
	}
	fieldsLen := len(db.Statement.Schema.Fields)
	fieldsByDBNameLen := len(db.Statement.Schema.FieldsByDBName)
	if fieldsLen*2 == fieldsByDBNameLen {
		//理论上，如果已经转换过了，那么应该满足这个条件
		return
	}
	var converter fieldConverter
	if config.FieldConvertType == Custom {
		//自定义转换
		converter = config.FieldConvertFunc
	} else {
		converter = config.FieldConvertType.convert
	}
	for _, field := range db.Statement.Schema.Fields {
		//将新的映射关系存入db.Statement.Schema.FieldsByDBName
		db.Statement.Schema.FieldsByDBName[converter(field.DBName)] = field
	}
}

func schemaFix(db *gorm.DB) {
	config := db.Dialector.(*Dialector).Config
	if config.Schema == "" || db.Statement.Table == "" || strings.Contains(db.Statement.Table, ".") {
		return
	}
	db.Statement.Table = strings.ToUpper(config.Schema) + "." + strings.ToUpper(db.Statement.Table)
}
