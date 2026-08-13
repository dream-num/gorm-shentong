package shentong

import (
	"database/sql/driver"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
)

func create(config *callbacks.Config) func(*gorm.DB) {
	defaultCreate := callbacks.Create(config)
	return func(db *gorm.DB) {
		if _, hasConflict := db.Statement.Clauses["ON CONFLICT"]; !hasConflict {
			defaultCreate(db)
			return
		}
		createWithMerge(db)
	}
}

// createWithMerge translates GORM's PostgreSQL-style ON CONFLICT clause to
// ShenTong MERGE statements. Like the official ShenTong dialect, slices are
// executed one row at a time because this MERGE form accepts one source row.
func createWithMerge(db *gorm.DB) {
	if db.Error != nil || db.Statement.SQL.Len() != 0 {
		return
	}
	if db.Statement.Schema != nil && !db.Statement.Unscoped {
		for _, schemaClause := range db.Statement.Schema.CreateClauses {
			db.Statement.AddClause(schemaClause)
		}
	}

	conflictClause, ok := db.Statement.Clauses["ON CONFLICT"]
	if !ok {
		return
	}
	onConflict, ok := conflictClause.Expression.(clause.OnConflict)
	if !ok {
		db.AddError(fmt.Errorf("shentong: unsupported ON CONFLICT expression %T", conflictClause.Expression))
		return
	}
	if onConflict.OnConstraint != "" {
		db.AddError(fmt.Errorf("shentong: ON CONSTRAINT %q cannot be converted to MERGE; specify conflict columns", onConflict.OnConstraint))
		return
	}
	if len(onConflict.TargetWhere.Exprs) != 0 {
		db.AddError(fmt.Errorf("shentong: partial-index TargetWhere cannot be converted to MERGE"))
		return
	}

	// This also expands UpdateAll and its default primary-key conflict target.
	values := callbacks.ConvertToCreateValues(db.Statement)
	if db.Error != nil {
		return
	}
	if updated, exists := db.Statement.Clauses["ON CONFLICT"]; exists {
		onConflict, _ = updated.Expression.(clause.OnConflict)
	}
	if len(values.Columns) == 0 || len(values.Values) == 0 {
		db.AddError(fmt.Errorf("shentong: MERGE requires explicit insert columns and values"))
		return
	}
	if len(onConflict.Columns) == 0 {
		db.AddError(fmt.Errorf("shentong: MERGE requires conflict columns"))
		return
	}
	if len(onConflict.Where.Exprs) != 0 {
		db.AddError(fmt.Errorf("shentong: conditional ON CONFLICT updates cannot be converted to MERGE USING DUAL"))
		return
	}

	stmt := db.Statement
	var rowsAffected int64
	for rowIndex, row := range values.Values {
		stmt.SQL.Reset()
		stmt.Vars = nil
		buildMergeSQL(db, values, row, onConflict)
		if db.Error != nil {
			return
		}
		if db.DryRun {
			return
		}
		result, err := stmt.ConnPool.ExecContext(stmt.Context, stmt.SQL.String(), stmt.Vars...)
		if err != nil {
			db.AddError(fmt.Errorf("shentong: MERGE row %d: %w", rowIndex, err))
			return
		}
		affected, _ := result.RowsAffected()
		rowsAffected += affected
	}
	db.RowsAffected = rowsAffected
}

func buildMergeSQL(db *gorm.DB, values clause.Values, row []interface{}, onConflict clause.OnConflict) {
	stmt := db.Statement
	stmt.WriteString("MERGE INTO ")
	stmt.WriteQuoted(clause.Table{Name: clause.CurrentTable})
	stmt.WriteString(" target USING DUAL ON (")
	for index, column := range onConflict.Columns {
		if index > 0 {
			stmt.WriteString(" AND ")
		}
		stmt.WriteQuoted(clause.Column{Table: "target", Name: column.Name, Raw: column.Raw})
		stmt.WriteByte('=')
		value, found := mergeColumnValue(values, row, column.Name)
		if !found {
			db.AddError(fmt.Errorf("shentong: conflict column %q is not part of the inserted values", column.Name))
			return
		}
		addMergeVar(stmt, value)
	}
	stmt.WriteByte(')')

	if !onConflict.DoNothing {
		stmt.WriteString(" WHEN MATCHED THEN UPDATE SET ")
		buildMergeAssignments(stmt, values, row, onConflict.DoUpdates)
	}

	stmt.WriteString(" WHEN NOT MATCHED THEN INSERT (")
	for index, column := range values.Columns {
		if index > 0 {
			stmt.WriteByte(',')
		}
		stmt.WriteQuoted(column)
	}
	stmt.WriteString(") VALUES (")
	for index := range values.Columns {
		if index > 0 {
			stmt.WriteByte(',')
		}
		addMergeVar(stmt, row[index])
	}
	stmt.WriteByte(')')
}

func buildMergeAssignments(stmt *gorm.Statement, values clause.Values, row []interface{}, assignments clause.Set) {
	if len(assignments) == 0 {
		column := clause.Column{Name: clause.PrimaryKey}
		stmt.WriteQuoted(column)
		stmt.WriteByte('=')
		stmt.WriteQuoted(column)
		return
	}
	for index, assignment := range assignments {
		if index > 0 {
			stmt.WriteByte(',')
		}
		// ShenTong, like Oracle, does not allow the target alias on the left
		// side of a MERGE UPDATE assignment.
		stmt.WriteQuoted(assignment.Column)
		stmt.WriteByte('=')
		if column, ok := assignment.Value.(clause.Column); ok && column.Table == "excluded" {
			value, found := mergeColumnValue(values, row, column.Name)
			if !found {
				stmt.AddError(fmt.Errorf("shentong: update column %q is not part of the inserted values", column.Name))
				return
			}
			addMergeVar(stmt, value)
		} else {
			addMergeVar(stmt, assignment.Value)
		}
	}
}

func mergeColumnValue(values clause.Values, row []interface{}, name string) (interface{}, bool) {
	for index, column := range values.Columns {
		if column.Name == name && index < len(row) {
			return row[index], true
		}
	}
	return nil, false
}

func addMergeVar(stmt *gorm.Statement, value interface{}) {
	stmt.AddVar(stmt, normalizeMergeValue(value))
}

// The ACI driver renders time.Time bind values as an unquoted timestamp inside
// MERGE statements. ShenTong then reports the hour (for example "09") as a
// syntax error. Binding the same value as this timestamp string is accepted and
// implicitly converted to TIMESTAMP by ShenTong.
func normalizeMergeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case time.Time:
		return typed.Format("2006-01-02 15:04:05.999999999")
	case *time.Time:
		if typed == nil {
			return nil
		}
		return typed.Format("2006-01-02 15:04:05.999999999")
	case driver.Valuer:
		converted, err := typed.Value()
		if err == nil {
			if convertedTime, ok := converted.(time.Time); ok {
				return convertedTime.Format("2006-01-02 15:04:05.999999999")
			}
		}
	}
	return value
}
