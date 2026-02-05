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
