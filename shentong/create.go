package shentong

import (
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/callbacks"
	"gorm.io/gorm/clause"
)

// mergeOnConflict translates GORM's PostgreSQL-style ON CONFLICT clause to a
// SQL-standard MERGE statement understood by ShenTong. It runs immediately
// before GORM's create callback; a non-empty Statement.SQL makes the standard
// callback execute this SQL without rebuilding INSERT ... ON CONFLICT.
func mergeOnConflict(db *gorm.DB) {
	if db.Error != nil || db.Statement.SQL.Len() != 0 {
		return
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

	stmt := db.Statement
	stmt.Vars = nil
	stmt.WriteString("MERGE INTO ")
	stmt.WriteQuoted(clause.Table{Name: clause.CurrentTable})
	stmt.WriteString(" target USING (VALUES ")
	for rowIndex, row := range values.Values {
		if rowIndex > 0 {
			stmt.WriteByte(',')
		}
		stmt.WriteByte('(')
		for columnIndex := range values.Columns {
			if columnIndex > 0 {
				stmt.WriteByte(',')
			}
			stmt.AddVar(stmt, row[columnIndex])
		}
		stmt.WriteByte(')')
	}
	stmt.WriteString(") excluded(")
	for index, column := range values.Columns {
		if index > 0 {
			stmt.WriteByte(',')
		}
		stmt.WriteQuoted(column)
	}
	stmt.WriteString(") ON (")
	for index, column := range onConflict.Columns {
		if index > 0 {
			stmt.WriteString(" AND ")
		}
		stmt.WriteQuoted(clause.Column{Table: "target", Name: column.Name, Raw: column.Raw})
		stmt.WriteByte('=')
		stmt.WriteQuoted(clause.Column{Table: "excluded", Name: column.Name, Raw: column.Raw})
	}
	stmt.WriteByte(')')

	if !onConflict.DoNothing {
		stmt.WriteString(" WHEN MATCHED THEN UPDATE SET ")
		buildMergeAssignments(stmt, onConflict.DoUpdates)
		if len(onConflict.Where.Exprs) != 0 {
			stmt.WriteString(" WHERE ")
			onConflict.Where.Build(stmt)
		}
	}

	stmt.WriteString(" WHEN NOT MATCHED THEN INSERT (")
	for index, column := range values.Columns {
		if index > 0 {
			stmt.WriteByte(',')
		}
		stmt.WriteQuoted(column)
	}
	stmt.WriteString(") VALUES (")
	for index, column := range values.Columns {
		if index > 0 {
			stmt.WriteByte(',')
		}
		stmt.WriteQuoted(clause.Column{Table: "excluded", Name: column.Name, Raw: column.Raw})
	}
	stmt.WriteByte(')')
}

func buildMergeAssignments(stmt *gorm.Statement, assignments clause.Set) {
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
		stmt.AddVar(stmt, assignment.Value)
	}
}
