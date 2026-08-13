package shentong

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/dream-num/gorm-shentong/oscar"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/migrator"
)

type Migrator struct {
	migrator.Migrator
}

type columnMetadata struct {
	Name       string        `gorm:"column:COLUMN_NAME"`
	DataType   string        `gorm:"column:DATA_TYPE"`
	Length     sql.NullInt64 `gorm:"column:CHAR_LENGTH"`
	Precision  sql.NullInt64 `gorm:"column:DATA_PRECISION"`
	Scale      sql.NullInt64 `gorm:"column:DATA_SCALE"`
	Nullable   string        `gorm:"column:NULLABLE"`
	PrimaryKey string        `gorm:"column:PRIMARY_KEY"`
}

func (m Migrator) ColumnTypes(value interface{}) ([]gorm.ColumnType, error) {
	var columnTypes []gorm.ColumnType
	err := m.RunWithValue(value, func(stmt *gorm.Statement) error {
		table := stmt.Table
		if parts := strings.Split(table, "."); len(parts) > 1 {
			table = parts[len(parts)-1]
		}

		var columns []columnMetadata
		err := m.DB.Raw(`
SELECT c.COLUMN_NAME,
       c.DATA_TYPE,
       c.CHAR_LENGTH,
       c.DATA_PRECISION,
       c.DATA_SCALE,
       c.NULLABLE,
       CASE WHEN EXISTS (
           SELECT 1
             FROM USER_CONS_COLUMNS cc
             JOIN USER_CONSTRAINTS con ON con.CONSTRAINT_NAME = cc.CONSTRAINT_NAME
            WHERE con.CONSTRAINT_TYPE = 'P'
              AND cc.TABLE_NAME = c.TABLE_NAME
              AND cc.COLUMN_NAME = c.COLUMN_NAME
       ) THEN 'Y' ELSE 'N' END AS PRIMARY_KEY
  FROM USER_TAB_COLUMNS c
 WHERE c.TABLE_NAME = ?
 ORDER BY c.COLUMN_ID`, strings.ToUpper(table)).Scan(&columns).Error
		if err != nil {
			return err
		}

		columnTypes = make([]gorm.ColumnType, 0, len(columns))
		for _, column := range columns {
			columnType := migrator.ColumnType{
				NameValue:        sql.NullString{String: column.Name, Valid: true},
				DataTypeValue:    sql.NullString{String: column.DataType, Valid: true},
				PrimaryKeyValue:  sql.NullBool{Bool: column.PrimaryKey == "Y", Valid: true},
				NullableValue:    sql.NullBool{Bool: column.Nullable == "Y", Valid: true},
				LengthValue:      column.Length,
				DecimalSizeValue: column.Precision,
				ScaleValue:       column.Scale,
			}
			columnTypes = append(columnTypes, columnType)
		}
		return nil
	})
	return columnTypes, err
}

func (m Migrator) CurrentDatabase() (name string) {
	baseName := m.Dialector.Name()
	m.DB.Raw(
		"SELECT OWNER FROM info_schem.all_tables WHERE OWNER LIKE ? ORDER BY OWNER=? DESC,OWNER limit 1",
		baseName+"%", baseName).Scan(&name)
	return
}

func (m Migrator) CreateTable(values ...interface{}) error {
	m.TryQuotifyReservedWords(values)
	m.TryRemoveOnUpdate(values)
	return m.Migrator.CreateTable(values...)
}

func (m Migrator) DropTable(values ...interface{}) error {
	values = m.ReorderModels(values, false)
	for i := len(values) - 1; i >= 0; i-- {
		value := values[i]
		tx := m.DB.Session(&gorm.Session{})
		if m.HasTable(value) {
			if err := m.RunWithValue(value, func(stmt *gorm.Statement) error {
				return tx.Exec("DROP TABLE ? CASCADE CONSTRAINTS", clause.Table{Name: stmt.Table}).Error
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m Migrator) HasTable(value interface{}) bool {
	var count int64

	m.RunWithValue(value, func(stmt *gorm.Statement) error {
		return m.DB.Raw("SELECT COUNT(*) FROM USER_TABLES WHERE TABLE_NAME = ?", stmt.Table).Row().Scan(&count)
	})

	return count > 0
}

func (m Migrator) RenameTable(oldName, newName interface{}) (err error) {
	resolveTable := func(name interface{}) (result string, err error) {
		if v, ok := name.(string); ok {
			result = v
		} else {
			stmt := &gorm.Statement{DB: m.DB}
			if err = stmt.Parse(name); err == nil {
				result = stmt.Table
			}
		}
		return
	}

	var oldTable, newTable string

	if oldTable, err = resolveTable(oldName); err != nil {
		return
	}

	if newTable, err = resolveTable(newName); err != nil {
		return
	}

	if !m.HasTable(oldTable) {
		return
	}

	return m.DB.Exec("RENAME TABLE ? TO ?",
		clause.Table{Name: oldTable},
		clause.Table{Name: newTable},
	).Error
}

func (m Migrator) AddColumn(value interface{}, field string) error {
	if !m.HasColumn(value, field) {
		return nil
	}

	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if field := stmt.Schema.LookUpField(field); field != nil {
			return m.DB.Exec(
				"ALTER TABLE ? ADD ? ?",
				clause.Table{Name: stmt.Table}, clause.Column{Name: field.DBName}, m.DB.Migrator().FullDataTypeOf(field),
			).Error
		}
		return fmt.Errorf("failed to look up field with name: %s", field)
	})
}

func (m Migrator) DropColumn(value interface{}, name string) error {
	if !m.HasColumn(value, name) {
		return nil
	}

	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if field := stmt.Schema.LookUpField(name); field != nil {
			name = field.DBName
		}

		return m.DB.Exec(
			"ALTER TABLE ? DROP ?",
			clause.Table{Name: stmt.Table},
			clause.Column{Name: name},
		).Error
	})
}

func (m Migrator) AlterColumn(value interface{}, field string) error {
	field = strings.ToUpper(field)
	if !m.HasColumn(value, field) {
		return nil
	}

	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if field := stmt.Schema.LookUpField(field); field != nil {
			return m.DB.Exec(
				"ALTER TABLE ? MODIFY ? ?",
				clause.Table{Name: stmt.Table},
				clause.Column{Name: field.DBName},
				m.FullDataTypeOf(field),
			).Error
		}
		return fmt.Errorf("failed to look up field with name: %s", field)
	})
}

func (m Migrator) HasColumn(value interface{}, field string) bool {
	var count int64
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		return m.DB.Raw("SELECT COUNT(*) FROM USER_TAB_COLUMNS WHERE TABLE_NAME = ? AND COLUMN_NAME = ?", stmt.Table, field).Row().Scan(&count)
	}) == nil && count > 0
}

func (m Migrator) CreateConstraint(value interface{}, name string) error {
	m.TryRemoveOnUpdate(value)
	return m.Migrator.CreateConstraint(value, name)
}

func (m Migrator) DropConstraint(value interface{}, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		for _, chk := range stmt.Schema.ParseCheckConstraints() {
			if chk.Name == name {
				return m.DB.Exec(
					"ALTER TABLE ? DROP CHECK ?",
					clause.Table{Name: stmt.Table}, clause.Column{Name: name},
				).Error
			}
		}

		return m.DB.Exec(
			"ALTER TABLE ? DROP CONSTRAINT ?",
			clause.Table{Name: stmt.Table}, clause.Column{Name: name},
		).Error
	})
}

func (m Migrator) HasConstraint(value interface{}, name string) bool {
	var count int64
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		return m.DB.Raw(
			"SELECT COUNT(*) FROM USER_CONSTRAINTS WHERE TABLE_NAME = ? AND CONSTRAINT_NAME = ?", stmt.Table, name,
		).Row().Scan(&count)
	}) == nil && count > 0
}

func (m Migrator) DropIndex(value interface{}, name string) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if idx := stmt.Schema.LookIndex(name); idx != nil {
			name = idx.Name
		}

		return m.DB.Exec("DROP INDEX ?", clause.Column{Name: name}, clause.Table{Name: stmt.Table}).Error
	})
}

func (m Migrator) HasIndex(value interface{}, name string) bool {
	var count int64
	m.RunWithValue(value, func(stmt *gorm.Statement) error {
		if idx := stmt.Schema.LookIndex(name); idx != nil {
			name = idx.Name
		}

		return m.DB.Raw(
			"SELECT COUNT(*) FROM USER_INDEXES WHERE TABLE_NAME = ? AND INDEX_NAME = ?",
			m.Migrator.DB.NamingStrategy.TableName(stmt.Table),
			m.Migrator.DB.NamingStrategy.IndexName(stmt.Table, name),
		).Row().Scan(&count)
	})

	return count > 0
}

// https://docs.oracle.com/database/121/SPATL/alter-index-rename.htm
func (m Migrator) RenameIndex(value interface{}, oldName, newName string) error {
	panic("TODO")
}

func (m Migrator) TryRemoveOnUpdate(value interface{}) error {
	return m.RunWithValue(value, func(stmt *gorm.Statement) error {
		for _, rel := range stmt.Schema.Relationships.Relations {
			constraint := rel.ParseConstraint()
			if constraint != nil {
				rel.Field.TagSettings["CONSTRAINT"] = strings.ReplaceAll(rel.Field.TagSettings["CONSTRAINT"], fmt.Sprintf("ON UPDATE %s", constraint.OnUpdate), "")
			}
		}
		return nil
	})
}

func (m Migrator) TryQuotifyReservedWords(values []interface{}) error {
	return m.RunWithValue(values, func(stmt *gorm.Statement) error {
		for idx, v := range stmt.Schema.DBNames {
			if oscar.IsReservedWord(v) {
				stmt.Schema.DBNames[idx] = fmt.Sprintf(`"%s"`, v)
			}
		}

		for _, v := range stmt.Schema.Fields {
			if oscar.IsReservedWord(v.DBName) {
				v.DBName = fmt.Sprintf(`"%s"`, v.DBName)
			}
		}
		return nil
	})
}
