package shentong

import (
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type permissionForMergeTest struct {
	PermID  string `gorm:"column:perm_id;primaryKey"`
	UnitID  string `gorm:"column:unit_id"`
	Object  string `gorm:"column:object"`
	Subject string `gorm:"column:subject"`
	Role    string `gorm:"column:role"`
	Deleted bool   `gorm:"column:deleted"`
}

func (permissionForMergeTest) TableName() string { return "permission" }

func openDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(New(Config{DSN: "test/test@127.0.0.1:2003/OSRDB", Schema: "universer"}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOnConflictIsBuiltAsMerge(t *testing.T) {
	db := openDryRunDB(t)
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "unit_id"}, {Name: "object"}, {Name: "subject"}},
		DoUpdates: clause.AssignmentColumns([]string{"deleted", "role"}),
	}).Create(&permissionForMergeTest{PermID: "p1", UnitID: "u1", Object: "o1", Subject: "s1", Role: "owner"})
	if result.Error != nil {
		t.Fatal(result.Error)
	}

	sql := result.Statement.SQL.String()
	for _, fragment := range []string{
		"MERGE INTO UNIVERSER.PERMISSION target",
		"USING DUAL ON (target.unit_id=:1 AND target.object=:2 AND target.subject=:3)",
		"WHEN MATCHED THEN UPDATE SET deleted=:4,role=:5",
		"WHEN NOT MATCHED THEN INSERT",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("SQL does not contain %q:\n%s", fragment, sql)
		}
	}
	if strings.Contains(sql, "ON CONFLICT") {
		t.Fatalf("PostgreSQL ON CONFLICT leaked into ShenTong SQL: %s", sql)
	}
	if len(result.Statement.Vars) != 11 {
		t.Fatalf("got %d bind variables, want 11", len(result.Statement.Vars))
	}
}

func TestOnConflictInlinesEmptyStrings(t *testing.T) {
	db := openDryRunDB(t)
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "unit_id"}, {Name: "object"}, {Name: "subject"}},
		DoUpdates: clause.AssignmentColumns([]string{"deleted", "role"}),
	}).Create(&permissionForMergeTest{PermID: "p1", UnitID: "u1", Object: "o1", Subject: "", Role: "owner"})
	if result.Error != nil {
		t.Fatal(result.Error)
	}

	sql := result.Statement.SQL.String()
	if !strings.Contains(sql, "target.subject=''") {
		t.Fatalf("empty conflict value was not inlined: %s", sql)
	}
	if !strings.Contains(sql, "VALUES (:5,:6,:7,'',:8,:9)") {
		t.Fatalf("empty insert value was not inlined: %s", sql)
	}
	if len(result.Statement.Vars) != 9 {
		t.Fatalf("got %d bind variables, want 9", len(result.Statement.Vars))
	}
}

func TestOnConflictDoNothingIsBuiltAsInsertOnlyMerge(t *testing.T) {
	db := openDryRunDB(t)
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "perm_id"}},
		DoNothing: true,
	}).Create(&permissionForMergeTest{PermID: "p1"})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	sql := result.Statement.SQL.String()
	if strings.Contains(sql, "WHEN MATCHED") {
		t.Fatalf("DoNothing MERGE must not have a matched action: %s", sql)
	}
	if !strings.Contains(sql, "WHEN NOT MATCHED THEN INSERT") {
		t.Fatalf("DoNothing MERGE has no insert action: %s", sql)
	}
}

func TestOnConflictUpdateAllUsesPrimaryKey(t *testing.T) {
	db := openDryRunDB(t)
	result := db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&permissionForMergeTest{PermID: "p1"})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	sql := result.Statement.SQL.String()
	if !strings.Contains(sql, "target.perm_id=:1") {
		t.Fatalf("UpdateAll did not use the primary key: %s", sql)
	}
}

func TestOnConflictBatchDryRunBuildsFirstMerge(t *testing.T) {
	db := openDryRunDB(t)
	permissions := []permissionForMergeTest{
		{PermID: "p1", UnitID: "u1"},
		{PermID: "p2", UnitID: "u2"},
	}
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "perm_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"unit_id"}),
	}).Create(&permissions)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if !strings.Contains(result.Statement.SQL.String(), "USING DUAL") {
		t.Fatalf("batch dry run did not build MERGE: %s", result.Statement.SQL.String())
	}
}

func TestNormalizeMergeTime(t *testing.T) {
	input := time.Date(2026, 8, 13, 17, 23, 45, 123456789, time.Local)
	if got, want := normalizeMergeValue(input), "2026-08-13 17:23:45.123456789"; got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}
