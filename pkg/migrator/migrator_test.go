package migrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListMigrationsSorting(t *testing.T) {
	// Setup temporary directory
	tempDir, err := os.MkdirTemp("", "migtest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create files in out-of-order sequence
	files := []string{
		"20240101120000_second.up.sql",
		"20240101120000_second.down.sql",
		"20231231120000_first.up.sql",
		"20231231120000_first.down.sql",
		"20240102120000_third.up.sql",
		"20240102120000_third.down.sql",
	}

	for _, f := range files {
		err := os.WriteFile(filepath.Join(tempDir, f), []byte("sql"), 0644)
		if err != nil {
			t.Fatal(err)
		}
	}

	m := NewMigrator(nil, tempDir)
	migrations, err := m.ListMigrations()
	if err != nil {
		t.Fatalf("ListMigrations failed: %v", err)
	}

	if len(migrations) != 3 {
		t.Errorf("Expected 3 migrations, got %d", len(migrations))
	}

	// Verify sorting
	expectedVersions := []string{"20231231120000", "20240101120000", "20240102120000"}
	for i, v := range expectedVersions {
		if migrations[i].Version != v {
			t.Errorf("At index %d: expected version %s, got %s", i, v, migrations[i].Version)
		}
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("MIG_TEST_PW", "s3cret")

	// SQL dollar syntax must survive: $$ dollar-quoting and $1 placeholders.
	in := []byte("DO $$ BEGIN CREATE ROLE r PASSWORD '${MIG_TEST_PW}'; END $$;\nDELETE FROM t WHERE v = $1;")
	got, err := expandEnv(in, "test.sql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "DO $$ BEGIN CREATE ROLE r PASSWORD 's3cret'; END $$;\nDELETE FROM t WHERE v = $1;"
	if string(got) != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}

	// Unset variable must fail loudly, not expand to "".
	if _, err := expandEnv([]byte("PASSWORD '${MIG_TEST_UNSET}'"), "test.sql"); err == nil {
		t.Error("expected error for unset variable, got nil")
	}
}

func TestExpandEnvIgnoresComments(t *testing.T) {
	t.Setenv("MIG_TEST_PW", "s3cret")

	cases := []struct {
		name string
		in   string
		want string
	}{{
		// A comment documenting the syntax must not be treated as a real
		// reference, even though no variable named VAR exists.
		name: "line comment is left alone",
		in:   "-- mig expands ${VAR} at run time\nPASSWORD '${MIG_TEST_PW}';",
		want: "-- mig expands ${VAR} at run time\nPASSWORD 's3cret';",
	}, {
		name: "block comment is left alone",
		in:   "/* see ${VAR} */ PASSWORD '${MIG_TEST_PW}';",
		want: "/* see ${VAR} */ PASSWORD 's3cret';",
	}, {
		name: "nested block comment is left alone",
		in:   "/* a /* ${VAR} */ b */ PASSWORD '${MIG_TEST_PW}';",
		want: "/* a /* ${VAR} */ b */ PASSWORD 's3cret';",
	}, {
		// A literal '--' must not be mistaken for a comment start; if it were,
		// the rest of the file would be skipped and nothing would expand.
		name: "double dash inside a string is not a comment",
		in:   "INSERT INTO t VALUES ('--'); PASSWORD '${MIG_TEST_PW}';",
		want: "INSERT INTO t VALUES ('--'); PASSWORD 's3cret';",
	}, {
		name: "escaped quote does not end the string early",
		in:   "INSERT INTO t VALUES ('it''s --'); PASSWORD '${MIG_TEST_PW}';",
		want: "INSERT INTO t VALUES ('it''s --'); PASSWORD 's3cret';",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := expandEnv([]byte(c.in), "test.sql")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}

	// An unset variable in real SQL must still fail, even with a comment present.
	if _, err := expandEnv([]byte("-- ${VAR}\nPASSWORD '${MIG_TEST_UNSET}';"), "test.sql"); err == nil {
		t.Error("expected error for unset variable outside comment, got nil")
	}
}
