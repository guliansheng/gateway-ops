package storage

import (
	"database/sql/driver"
	"strings"
	"testing"
)

func degradationHasTableStep() degradationSQLStep {
	return degradationSQLStep{contains: "tables", columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}
}

func degradationConstraintDefinitionStep(definition string) degradationSQLStep {
	return degradationSQLStep{contains: "pg_get_constraintdef", columns: []string{"definition"}, rows: [][]driver.Value{{definition}}}
}

// A database created before the combined schedule existed still carries the
// pelican/candy-only CHECK constraint and rejected saving "鹈鹕和糖果".
func TestMigrateDegradationMethodConstraintWidensLegacyCheck(t *testing.T) {
	legacy := "CHECK (((method)::text = ANY ((ARRAY['pelican'::character varying, 'candy'::character varying])::text[])))"
	var statements []string
	record := func(step degradationSQLStep) degradationSQLStep {
		step.check = func(query string, _ []driver.NamedValue) { statements = append(statements, query) }
		return step
	}
	steps := []degradationSQLStep{
		degradationHasTableStep(),
		degradationConstraintDefinitionStep(legacy),
		record(degradationSQLStep{contains: "DROP CONSTRAINT IF EXISTS chk_group_degradation_method"}),
		record(degradationSQLStep{contains: "ADD CONSTRAINT chk_group_degradation_method"}),
	}
	repo, _ := newDegradationSQL(t, steps...)
	if err := migrateDegradationMethodConstraint(repo.db); err != nil {
		t.Fatalf("migration error = %v", err)
	}
	if len(statements) != 2 || !strings.Contains(statements[0], "DROP CONSTRAINT") || !strings.Contains(statements[1], "'both'") {
		t.Fatalf("statements=%+v", statements)
	}
}

func TestMigrateDegradationMethodConstraintLeavesWidenedCheck(t *testing.T) {
	widened := "CHECK (((method)::text = ANY ((ARRAY['pelican'::character varying, 'candy'::character varying, 'both'::character varying])::text[])))"
	repo, state := newDegradationSQL(t, degradationHasTableStep(), degradationConstraintDefinitionStep(widened))
	if err := migrateDegradationMethodConstraint(repo.db); err != nil {
		t.Fatalf("migration error = %v", err)
	}
	if len(state.steps) != 0 {
		t.Fatalf("widened constraint rewritten: %d steps left", len(state.steps))
	}
}
