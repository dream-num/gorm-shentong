package shentong

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/Mystery00/gorm-shentong/oscar"
	shentongdriver "github.com/team-ide/go-driver/db_shentong"
	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/migrator"
	"gorm.io/gorm/schema"
)

type Config struct {
	DriverName        string
	DSN               string
	Schema            string
	DSNConfig         *oscar.Config
	Conn              *sql.DB
	DefaultStringSize uint
	FieldConvertType  FieldConvertType
	FieldConvertFunc  func(string) string
}

func Init() {
	// db_shentong 在包初始化时注册 ACI 驱动，保留此方法兼容已有调用方。
}

type Dialector struct {
	*Config
}

func Open(dsn string) gorm.Dialector {
	return New(Config{DSN: dsn})
}

func New(config Config) gorm.Dialector {
	return &Dialector{
		Config: &config,
	}
}

func (d Dialector) Name() string {
	return "shentong"
}

func (d Dialector) Initialize(db *gorm.DB) (err error) {
	// register callbacks
	callbacks.RegisterDefaultCallbacks(db, &callbacks.Config{LastInsertIDReversed: true})

	d.DriverName = shentongdriver.GetDriverName()

	if d.DSN == "" {
		d.DSN = d.DSNConfig.FormatDSN()
	}
	if d.Conn != nil {
		db.ConnPool = d.Conn
	} else {
		db.ConnPool, err = sql.Open(d.DriverName, d.DSN)
		if err != nil {
			return
		}
	}

	// 在所有查询之前，设置一下钩子，用于动态替换字段名称的命名
	if d.FieldConvertType != None {
		// 没有配置，那么自然不需要注册钩子
		if err = db.Callback().Query().Before("*").Register("shentong_query", queryFix); err != nil {
			return err
		}
	}
	if d.Schema != "" {
		if err = db.Callback().Create().Before("*").Register("shentong_schema_create", schemaFix); err != nil {
			return err
		}
		if err = db.Callback().Query().Before("*").Register("shentong_schema_query", schemaFix); err != nil {
			return err
		}
		if err = db.Callback().Update().Before("*").Register("shentong_schema_update", schemaFix); err != nil {
			return err
		}
		if err = db.Callback().Delete().Before("*").Register("shentong_schema_delete", schemaFix); err != nil {
			return err
		}
	}

	return
}

func (d Dialector) Migrator(db *gorm.DB) gorm.Migrator {
	// TODO 注，这个东西因为我自己没有用到，所以没有做适配处理
	return Migrator{
		Migrator: migrator.Migrator{
			Config: migrator.Config{
				DB:                          db,
				Dialector:                   d,
				CreateIndexAfterCreateTable: true,
			},
		},
	}
}

func (d Dialector) DataTypeOf(field *schema.Field) string {
	switch field.DataType {
	case schema.Bool:
		return "BOOLEAN"
	case schema.Int, schema.Uint:
		return d.getSchemaIntAndUnitType(field)
	case schema.Float:
		return d.getSchemaFloatType(field)
	case schema.String:
		return d.getSchemaStringType(field)
	case schema.Time:
		return d.getSchemaTimeType(field)
	case schema.Bytes:
		return "BLOB"
	default:
		return d.getSchemaCustomType(field)
	}
}

func (d Dialector) getSchemaFloatType(field *schema.Field) string {
	if field.Precision > 0 {
		return fmt.Sprintf("DECIMAL(%d, %d)", field.Precision, field.Scale)
	}

	if field.Size <= 32 {
		return "FLOAT"
	}

	return "DOUBLE"
}

func (d Dialector) getSchemaStringType(field *schema.Field) string {
	size := field.Size
	if size >= 8000 {
		return "TEXT"
	}

	return fmt.Sprintf("VARCHAR(%d)", size)
}

func (d Dialector) getSchemaTimeType(field *schema.Field) string {
	if field.NotNull || field.PrimaryKey {
		return "TIMESTAMP NOT NULL"
	}
	return "TIMESTAMP NULL"
}

func (d Dialector) getSchemaIntAndUnitType(field *schema.Field) string {
	sqlType := "BIGINT"
	switch {
	case field.Size <= 8:
		sqlType = "TINYINT"
	case field.Size <= 16:
		sqlType = "SMALLINT"
	case field.Size <= 24:
		sqlType = "INT"
	}

	if field.AutoIncrement {
		sqlType += " AUTO_INCREMENT"
	}

	return sqlType
}

func (d Dialector) getSchemaCustomType(field *schema.Field) string {
	sqlType := string(field.DataType)

	if field.AutoIncrement && !strings.Contains(strings.ToLower(sqlType), " auto_increment") {
		sqlType += " AUTO_INCREMENT"
	}

	return sqlType
}

func (d Dialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "VALUES (DEFAULT)"}
}

func (d Dialector) BindVarTo(writer clause.Writer, stmt *gorm.Statement, v interface{}) {
	writer.WriteString(":")
	writer.WriteString(strconv.Itoa(len(stmt.Vars)))
}

func (d Dialector) QuoteTo(writer clause.Writer, str string) {
	writer.WriteString(str)
}

func (d Dialector) Explain(sql string, vars ...interface{}) string {
	return logger.ExplainSQL(sql, nil, `'`, vars...)
}
