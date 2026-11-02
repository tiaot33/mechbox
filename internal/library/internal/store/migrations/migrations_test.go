package migrations

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsLoad(t *testing.T) {
	if _, err := Load(27); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOrdersSQLAndGoStepsByVersion(t *testing.T) {
	up := func(context.Context, *sql.Tx) error { return nil }
	steps, err := load(fstest.MapFS{
		"migrations.go":           {},
		"migrations_test.go":      {},
		"helpers.go":              {},
		".DS_Store":               {},
		".00028_add_pins.sql.swp": {},
		"README.md":               {},
		"00029_backfill_notes.go": {},
		"00028_add_pins.sql":      {Data: []byte("CREATE TABLE pins (id INTEGER PRIMARY KEY);")},
	}, map[string]func(context.Context, *sql.Tx) error{"00029_backfill_notes.go": up}, 27)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(steps))
	for i, step := range steps {
		got[i] = step.Name
	}
	if want := []string{"add_pins", "backfill_notes"}; !reflect.DeepEqual(got, want) || steps[0].Version != 28 || steps[0].SQL == "" || steps[1].Version != 29 || steps[1].Go == nil {
		t.Fatalf("steps = %+v; want add_pins (28, SQL) then backfill_notes (29, Go)", steps)
	}
}

func TestLoadRejectsBadMigrationFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"gap", fstest.MapFS{"00028_a.sql": {}, "00030_c.sql": {}}, "want version 00029"},
		{"starts too late", fstest.MapFS{"00029_a.sql": {}}, "want version 00028"},
		{"duplicate version", fstest.MapFS{"00028_a.sql": {}, "00028_b.sql": {}}, "want version 00029"},
		{"bad name", fstest.MapFS{"28_add.sql": {}}, "want NNNNN_name.sql"},
		{"capital letters", fstest.MapFS{"00028_AddPins.sql": {}}, "want NNNNN_name.sql"},
		{"double underscore", fstest.MapFS{"00028_add__pins.go": {}}, "want NNNNN_name.sql"},
		{"go step without register", fstest.MapFS{"00028_backfill.go": {}}, "does not call register"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(tc.files, nil, 27)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("load error = %v; want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestFileNameUsesTheLoadedNamingRule(t *testing.T) {
	file, err := FileName(28, "add_pins")
	if err != nil || file != "00028_add_pins.sql" {
		t.Fatalf("FileName = %q, %v; want 00028_add_pins.sql", file, err)
	}
	for _, name := range []string{"AddPins", "add-pins", "add__pins", "_add", ""} {
		if _, err := FileName(28, name); err == nil {
			t.Errorf("FileName(%q) succeeded; want an error", name)
		}
	}
}
